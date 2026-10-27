//go:build windows

package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

type terminalLaunchRecord struct {
	Args          []string `json:"args"`
	ConsoleInput  bool     `json:"console_input"`
	ConsoleOutput bool     `json:"console_output"`
}

// A copy of this test executable acts as ssh.exe. TestMain runs before test-flag
// parsing, so it can record OpenSSH arguments without making a network request.
func TestMain(m *testing.M) {
	if os.Getenv("DGX_HUB_TERMINAL_HELPER") == "wt-error" {
		fmt.Fprintln(os.Stderr, "test Windows Terminal activation error")
		os.Exit(7)
	}
	if os.Getenv("DGX_HUB_TERMINAL_HELPER") == "1" {
		getStdHandle := kernel32.NewProc("GetStdHandle")
		getConsoleMode := kernel32.NewProc("GetConsoleMode")
		consoleHandle := func(kind uintptr) bool {
			handle, _, _ := getStdHandle.Call(kind)
			var mode uint32
			ok, _, _ := getConsoleMode.Call(handle, uintptr(unsafe.Pointer(&mode)))
			return ok != 0
		}
		record := terminalLaunchRecord{Args: os.Args[1:], ConsoleInput: consoleHandle(0xfffffff6), ConsoleOutput: consoleHandle(0xfffffff5)}
		data, err := json.Marshal(record)
		if err != nil {
			os.Exit(2)
		}
		if err := os.WriteFile(os.Getenv("DGX_HUB_TERMINAL_RECORD"), data, 0o600); err != nil {
			os.Exit(3)
		}
		if holdMS, _ := strconv.Atoi(os.Getenv("DGX_HUB_TERMINAL_HOLD_MS")); holdMS > 0 {
			deadline := time.Now().Add(time.Duration(holdMS) * time.Millisecond)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(os.Getenv("DGX_HUB_TERMINAL_RECORD") + ".stop"); err == nil {
					break
				}
				time.Sleep(25 * time.Millisecond)
			}
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestWindowsTerminalCommandIsVisibleAndUsesNewWindow(t *testing.T) {
	const terminal = `C:\WindowsApps\wt.exe`
	const ssh = `C:\Program Files\OpenSSH\ssh.exe`
	const target = "test-user@example.invalid"
	cmd := windowsTerminalCommand(terminal, ssh, target)
	want := []string{terminal, "-w", "new", "new-tab", "--title", "DGX Hub - " + target, "--", ssh, "-tt", "--", target}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("terminal arguments changed: %#v", cmd.Args)
	}
	if cmd.SysProcAttr != nil && (cmd.SysProcAttr.HideWindow || cmd.SysProcAttr.CreationFlags&createNoWindow != 0) {
		t.Fatal("Windows Terminal was launched with hidden/background flags")
	}
}

func TestWindowsTerminalEarlyFailureFallsBack(t *testing.T) {
	var fallbackCalls, terminalCalls int
	err := launchTerminal("test-user@example.invalid", terminalLaunchers{
		lookup: func(name string) (string, error) { return `C:\Tools\` + name, nil },
		windowsTerminal: func(cmd *exec.Cmd) error {
			terminalCalls++
			if cmd.SysProcAttr != nil && cmd.SysProcAttr.HideWindow {
				t.Fatal("terminal must be visible")
			}
			return errors.New("activation denied")
		},
		console: func(executable string, args []string) error {
			fallbackCalls++
			if executable != `C:\Tools\ssh.exe` || !reflect.DeepEqual(args, []string{"-tt", "--", "test-user@example.invalid"}) {
				t.Fatalf("incorrect fallback: %q %#v", executable, args)
			}
			return nil
		},
	})
	if err != nil || terminalCalls != 1 || fallbackCalls != 1 {
		t.Fatalf("expected working fallback: %v; WT=%d console=%d", err, terminalCalls, fallbackCalls)
	}
}

func TestTerminalLaunchReportsBothFailures(t *testing.T) {
	err := launchTerminal("elly", terminalLaunchers{
		lookup:          func(name string) (string, error) { return name, nil },
		windowsTerminal: func(*exec.Cmd) error { return errors.New("terminal activation denied") },
		console:         func(string, []string) error { return errors.New("console access denied") },
	})
	if err == nil || !strings.Contains(err.Error(), "terminal activation denied") || !strings.Contains(err.Error(), "console access denied") {
		t.Fatalf("missing launcher diagnostics: %v", err)
	}
}

func TestWindowsTerminalExitFailureIsObserved(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable)
	cmd.Env = append(os.Environ(), "DGX_HUB_TERMINAL_HELPER=wt-error")
	// This console test helper is not the GUI command above. Hide the helper
	// itself so normal tests don't flash a console; the real GUI flags have a
	// separate regression assertion.
	Background(cmd)
	err = launchWindowsTerminal(cmd)
	if err == nil || !strings.Contains(err.Error(), "exit status 7") || !strings.Contains(err.Error(), "test Windows Terminal activation error") {
		t.Fatalf("launcher exit error was ignored: %v", err)
	}
}

// This regression test uses the installed Windows Terminal and a fake SSH
// child. IsWindowVisible detects the original SW_HIDE bug even when wt.exe
// returns success and the child starts correctly. No SSH node is contacted.
func TestWindowsTerminalOpensVisibleSSHWindow(t *testing.T) {
	if os.Getenv("DGX_HUB_TEST_TERMINAL") != "1" {
		t.Skip("set DGX_HUB_TEST_TERMINAL=1 on a Windows desktop")
	}
	if _, err := exec.LookPath("wt.exe"); err != nil {
		t.Skip("Windows Terminal is not installed")
	}
	base := t.TempDir()
	helperDir := filepath.Join(base, "SSH helper with spaces")
	if err := os.Mkdir(helperDir, 0o700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helper, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(helperDir, "ssh.exe"), helper, 0o700); err != nil {
		t.Fatal(err)
	}
	recordPath := filepath.Join(base, "launch.json")
	t.Setenv("PATH", helperDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DGX_HUB_TERMINAL_HELPER", "1")
	t.Setenv("DGX_HUB_TERMINAL_RECORD", recordPath)
	t.Setenv("DGX_HUB_TERMINAL_HOLD_MS", "10000")
	target := fmt.Sprintf("dgx-hub-wt-visible-%d.invalid", time.Now().UnixNano())
	title := "DGX Hub - " + target
	enumWindows := user32.NewProc("EnumWindows")
	isWindowVisible := user32.NewProc("IsWindowVisible")
	getWindowTextLength := user32.NewProc("GetWindowTextLengthW")
	getWindowText := user32.NewProc("GetWindowTextW")
	var visibleWindow uintptr
	callback := syscall.NewCallback(func(window, _ uintptr) uintptr {
		visible, _, _ := isWindowVisible.Call(window)
		if visible == 0 {
			return 1
		}
		length, _, _ := getWindowTextLength.Call(window)
		if length == 0 {
			return 1
		}
		text := make([]uint16, length+1)
		getWindowText.Call(window, uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)))
		if syscall.UTF16ToString(text) == title {
			visibleWindow = window
			return 0
		}
		return 1
	})
	t.Cleanup(func() {
		// Tell the helper to exit cleanly; never terminate the shared Terminal
		// process, which can also contain the user's unrelated windows.
		_ = os.WriteFile(recordPath+".stop", nil, 0o600)
		if visibleWindow != 0 {
			procPostMessage.Call(visibleWindow, wmClose, 0, 0)
		}
	})
	if err := LaunchTerminal(target); err != nil {
		t.Fatal(err)
	}
	var record terminalLaunchRecord
	recordReady := false
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if !recordReady {
			if data, err := os.ReadFile(recordPath); err == nil && json.Unmarshal(data, &record) == nil {
				recordReady = true
			}
		}
		enumWindows.Call(callback, 0)
		if recordReady && visibleWindow != 0 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !recordReady {
		t.Fatal("Windows Terminal did not start the fake SSH child")
	}
	if !reflect.DeepEqual(record.Args, []string{"-tt", "--", target}) {
		t.Fatalf("SSH arguments changed: %#v", record.Args)
	}
	if !record.ConsoleInput || !record.ConsoleOutput {
		t.Fatalf("SSH did not receive console handles: %#v", record)
	}
	if visibleWindow == 0 {
		t.Fatal("SSH started but Windows Terminal did not expose the requested visible window")
	}
}

// This desktop-only test opens a fresh native console briefly. The fake SSH
// exits immediately after recording argv and console handles; no host is used.
func TestTerminalFallbackStartsInteractiveConsole(t *testing.T) {
	if os.Getenv("DGX_HUB_TEST_TERMINAL") != "1" {
		t.Skip("set DGX_HUB_TEST_TERMINAL=1 on a Windows desktop")
	}
	base := t.TempDir()
	helperDir := filepath.Join(base, "SSH helper with spaces")
	if err := os.Mkdir(helperDir, 0o700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helper, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(helperDir, "ssh.exe"), helper, 0o700); err != nil {
		t.Fatal(err)
	}
	recordPath := filepath.Join(base, "launch.json")
	t.Setenv("PATH", helperDir) // Excludes wt.exe and forces the documented fallback.
	t.Setenv("DGX_HUB_TERMINAL_HELPER", "1")
	t.Setenv("DGX_HUB_TERMINAL_RECORD", recordPath)
	const target = "test-user@[::1]"
	if err := LaunchTerminal(target); err != nil {
		t.Fatal(err)
	}
	var record terminalLaunchRecord
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, readErr := os.ReadFile(recordPath)
		if readErr == nil && json.Unmarshal(data, &record) == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fake SSH did not report its launch: %v", readErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !reflect.DeepEqual(record.Args, []string{"-tt", "--", target}) {
		t.Fatalf("OpenSSH argv was changed: %#v", record.Args)
	}
	if !record.ConsoleInput || !record.ConsoleOutput {
		t.Fatalf("SSH did not receive interactive console handles: %#v", record)
	}
}
