package sshutil

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestValidateTargetRejectsOptionsAndShellSyntax(t *testing.T) {
	for _, target := range []string{"elly", "user@10.0.0.2", "user-name@elly.local", "evie_ssh", "user@2001:db8::1", "::1", "user@::1", "[::1]"} {
		if err := ValidateTarget(target); err != nil {
			t.Errorf("valid target %q: %v", target, err)
		}
	}
	for _, target := range []string{"", "-oProxyCommand=calc", "user@-bad", " elly", "elly\ntrue", "elly;echo hi", "elly$(whoami)", "user@host@other", "user name@host", "host/command", "host`cmd`"} {
		if err := ValidateTarget(target); err == nil {
			t.Errorf("unsafe target accepted: %q", target)
		}
	}
}
func TestValidateRemoteHost(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "localhost", "service.internal", "2001:db8::1", "[::1]", "container_name"} {
		if err := ValidateRemoteHost(host); err != nil {
			t.Errorf("valid remote host %q: %v", host, err)
		}
	}
	for _, host := range []string{"", "-L", "user@host", "host:80", "host;id", "host/dir", "host\nother", "a..b"} {
		if err := ValidateRemoteHost(host); err == nil {
			t.Errorf("invalid remote host accepted %q", host)
		}
	}
}
func TestClassifyActionableSSHErrors(t *testing.T) {
	tests := []struct{ stderr, code string }{
		{"user@elly: Permission denied (publickey,password).", "authentication_required"},
		{"Host key verification failed.", "host_key_required"},
		{"WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!", "host_key_required"},
		{"ssh: connect to host elly port 22: Connection refused", "network_error"},
		{"ssh: Could not resolve hostname evie", "network_error"},
	}
	for _, tc := range tests {
		code, message := ClassifyError(errors.New("exit status 255"), tc.stderr)
		if code != tc.code || message == "" {
			t.Errorf("%q: code=%q message=%q", tc.stderr, code, message)
		}
	}
	code, _ := ClassifyError(&exec.Error{Name: "ssh", Err: exec.ErrNotFound}, "")
	if code != "ssh_missing" {
		t.Fatalf("missing SSH code %s", code)
	}
	code, _ = ClassifyError(context.DeadlineExceeded, "")
	if code != "timeout" {
		t.Fatalf("timeout code %s", code)
	}
}
func TestCheckRejectsTargetBeforeLaunchingSSH(t *testing.T) {
	result := Check(context.Background(), "-oProxyCommand=unsafe")
	if result.OK || result.Code != "invalid_target" {
		t.Fatalf("unexpected result: %+v", result)
	}
}
func TestSetupDoesNotSuggestBypassingHostKeysOrExposeSecrets(t *testing.T) {
	for _, step := range SetupSteps("user@elly") {
		if strings.Contains(step.Command, "StrictHostKeyChecking=no") || strings.Contains(step.Command, "sshpass") {
			t.Fatalf("unsafe step: %+v", step)
		}
	}
}
func TestBoundedOutput(t *testing.T) {
	var b Buffer
	p := strings.Repeat("x", 100000)
	n, err := b.Write([]byte(p))
	if n != len(p) || err != nil || len(b.String()) != 64*1024 {
		t.Fatal("output limit failed")
	}
}

func TestBackgroundConfigurationPreventsInheritedForwardsAndDetach(t *testing.T) {
	if err := parseBackgroundConfig("hostname elly\nuser ai\n"); err != nil {
		t.Fatal(err)
	}
	if err := parseBackgroundConfig("hostname elly\nforkafterauthentication no\nsessiontype default\nstdinnull no\n"); err != nil {
		t.Fatal(err)
	}
	for _, directive := range []string{"localforward 0.0.0.0:8080 localhost:80", "remoteforward *:8080 localhost:80", "dynamicforward *:1080", "forkafterauthentication yes", "stdinnull yes", "sessiontype none"} {
		err := parseBackgroundConfig("hostname elly\n" + directive + "\n")
		if err == nil {
			t.Errorf("unsafe background SSH configuration accepted: %s", directive)
			continue
		}
		code, _ := ClassifyError(err, "")
		if code != "ssh_config_error" {
			t.Errorf("configuration issue lost its category: %s", code)
		}
	}
	if err := parseBackgroundConfig("unexpected output\n"); err == nil {
		t.Fatal("invalid effective config accepted")
	}
}

func TestCommandBoundsInheritedPipeWait(t *testing.T) {
	cmd := Command(context.Background(), "-V")
	if cmd.WaitDelay <= 0 {
		t.Fatal("SSH can wait forever for inherited stdio pipes")
	}
}
