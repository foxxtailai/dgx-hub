package sshutil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// ActionArgs constructs an explicitly noninteractive, independent Linux shell.
// Each invocation starts in HOME with a small fixed environment. The command is
// deliberately user-authored shell code, quoted as one remote sh -c argument.
func ActionArgs(target, command string) []string {
	command = strings.ReplaceAll(command, "\r\n", "\n")
	remote := `cd "$HOME" && exec env -i HOME="$HOME" USER="${USER:-}" LOGNAME="${LOGNAME:-}" PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin LANG=C /bin/sh -c ` + ShellQuote(command) + ` </dev/null`
	return append(BaseArgs(), "-o", "ClearAllForwardings=yes", "-o", "ServerAliveInterval=5", "-o", "ServerAliveCountMax=2", target, remote)
}

// RunAction reuses DGX Hub's managed system OpenSSH process and trust policy.
// Cancellation closes the local SSH operation; it cannot guarantee termination
// of remote jobs that detach or ignore session closure.
func RunAction(ctx context.Context, target, command string, stdout, stderr io.Writer) (*int, error) {
	if err := ValidateBackgroundConfig(ctx, target); err != nil {
		return nil, err
	}
	cmd := Command(ctx, ActionArgs(target, command)...)
	var diagnostic Buffer
	cmd.Stdout, cmd.Stderr = stdout, io.MultiWriter(stderr, &diagnostic)
	// A nil Stdin is the local null device; the remote shell also redirects
	// stdin so neither the command nor sudo can wait for interactive input.
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err == nil {
		zero := 0
		return &zero, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code := exit.ExitCode()
		if code != 255 {
			return &code, fmt.Errorf("command exited with code %d", code)
		}
		_, message := ClassifyError(err, diagnostic.String())
		return &code, fmt.Errorf("SSH connection failed: %s", message)
	}
	_, message := ClassifyError(err, diagnostic.String())
	return nil, errors.New(message)
}
