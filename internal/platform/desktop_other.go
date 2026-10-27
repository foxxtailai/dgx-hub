//go:build !windows

package platform

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

func Background(cmd *exec.Cmd) {}

func OpenBrowser(rawURL string) error {
	if err := validateBrowserURL(rawURL); err != nil {
		return err
	}
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return fmt.Errorf("no graphical desktop detected; open %s on this machine", rawURL)
	}
	cmd := exec.Command("xdg-open", rawURL)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open desktop browser: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func LaunchTerminal(target string) error {
	if err := validateTerminalTarget(target); err != nil {
		return err
	}
	return fmt.Errorf("%w; Linux terminal integration is deferred. Run this command on the client machine: ssh -- %s", ErrTerminalUnavailable, target)
}

func RunTray(ctx context.Context, rawURL string, onExit func()) error {
	return ErrTrayUnavailable
}

func ShowError(title, message string) {
	fmt.Fprintf(os.Stderr, "%s: %s\n", title, message)
}
