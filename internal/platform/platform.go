// Package platform contains desktop integration, separated from DGX Hub's core.
package platform

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

var (
	ErrTrayUnavailable     = errors.New("system tray integration is currently available on Windows 10 and 11 only")
	ErrTerminalUnavailable = errors.New("no supported graphical terminal is available")
	terminalTargetPattern  = regexp.MustCompile(`^[A-Za-z0-9_\[:][A-Za-z0-9_.@:%+\[\]-]*$`)
)

// DataDir returns the current user's application-data directory, never the
// executable directory. The caller creates it when opening storage.
func DataDir() (string, error) {
	base, err := userDataBase()
	if err != nil {
		return "", err
	}
	name := "dgx-hub"
	if runtime.GOOS == "windows" {
		name = "DGXHub"
	}
	return filepath.Join(base, name), nil
}

// LegacyDatabasePath is used only to import settings from the previous app name.
func LegacyDatabasePath(customDir string) (string, error) {
	if customDir != "" {
		return filepath.Join(customDir, "foxhub.db"), nil
	}
	base, err := userDataBase()
	if err != nil {
		return "", err
	}
	name := "foxhub"
	if runtime.GOOS == "windows" {
		name = "FoxHub"
	}
	return filepath.Join(base, name, "foxhub.db"), nil
}

func userDataBase() (string, error) {
	if runtime.GOOS == "windows" {
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			var err error
			base, err = os.UserCacheDir()
			if err != nil {
				return "", fmt.Errorf("locate Windows application data: %w", err)
			}
		}
		if !filepath.IsAbs(base) {
			return "", errors.New("LOCALAPPDATA must be an absolute directory")
		}
		return base, nil
	}
	if base := os.Getenv("XDG_DATA_HOME"); base != "" && filepath.IsAbs(base) {
		return base, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share"), nil
}

func validateTerminalTarget(target string) error {
	if len(target) > 255 || !terminalTargetPattern.MatchString(target) {
		return errors.New("invalid SSH target: use an SSH alias, hostname, IP address, or user@host")
	}
	return nil
}

func validateBrowserURL(rawURL string) error {
	if strings.ContainsAny(rawURL, "\x00\r\n") {
		return errors.New("invalid browser URL")
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("browser URL must be an absolute HTTP or HTTPS address")
	}
	return nil
}
