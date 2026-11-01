package sshutil

import (
	"strings"
	"testing"
)

func TestActionArgsPreserveTrustAndIsolateCommand(t *testing.T) {
	command := "printf '%s\\n' \"$HOME\"\r\ncd /tmp; echo 'Привет 世界'; echo literal"
	args := ActionArgs("chosen-node", command)
	joined := strings.Join(args[:len(args)-2], " ")
	for _, option := range []string{"-T", "BatchMode=yes", "StrictHostKeyChecking=yes", "ControlMaster=no", "ControlPath=none", "ControlPersist=no", "PermitLocalCommand=no", "ForwardAgent=no", "ClearAllForwardings=yes", "ServerAliveInterval=5", "ServerAliveCountMax=2"} {
		if !strings.Contains(joined, option) {
			t.Errorf("missing %s in %v", option, args)
		}
	}
	if args[len(args)-2] != "chosen-node" {
		t.Fatalf("target and command are not separate arguments: %v", args)
	}
	remote := args[len(args)-1]
	if !strings.HasPrefix(remote, `cd "$HOME" && exec env -i HOME="$HOME"`) || !strings.HasSuffix(remote, ` </dev/null`) || !strings.Contains(remote, `LANG=C /bin/sh -c `+ShellQuote(strings.ReplaceAll(command, "\r\n", "\n"))) {
		t.Fatalf("command lacks explicit independent environment or quoting: %s", remote)
	}
	if strings.Contains(remote, "\r") {
		t.Fatal("Windows command newlines were not normalized")
	}
}
