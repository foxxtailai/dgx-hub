//go:build windows

package platform

import (
	"errors"
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	createNoWindow   = 0x08000000
	createNewConsole = 0x00000010
)

var (
	shell32            = syscall.NewLazyDLL("shell32.dll")
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procShellExecute   = shell32.NewProc("ShellExecuteW")
	procCreateProcess  = kernel32.NewProc("CreateProcessW")
	ole32              = syscall.NewLazyDLL("ole32.dll")
	procCoInitialize   = ole32.NewProc("CoInitializeEx")
	procCoUninitialize = ole32.NewProc("CoUninitialize")
	procMessageBox     = user32.NewProc("MessageBoxW")
)

// Background hides console windows for managed telemetry and tunnel processes.
func Background(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}

func OpenBrowser(rawURL string) error {
	if err := validateBrowserURL(rawURL); err != nil {
		return err
	}
	// Shell URL handlers can use COM. Keep initialization and ShellExecute on
	// the same thread, including when called from the tray's worker goroutine.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	initialized, _, _ := procCoInitialize.Call(0, 0x2|0x4)
	if uint32(initialized) <= 1 {
		defer procCoUninitialize.Call()
	} else if uint32(initialized) != 0x80010106 { // RPC_E_CHANGED_MODE: COM already initialized.
		return fmt.Errorf("initialize desktop browser integration: HRESULT 0x%x", uint32(initialized))
	}
	verb, _ := syscall.UTF16PtrFromString("open")
	address, _ := syscall.UTF16PtrFromString(rawURL)
	result, _, _ := procShellExecute.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(address)), 0, 0, 1)
	if result <= 32 {
		return fmt.Errorf("open default browser: Windows shell returned error %d", result)
	}
	return nil
}

// ShowError makes startup failures visible even in a windowless release build.
// It is reserved for fatal application errors, not routine SSH status messages.
func ShowError(title, message string) {
	titlePointer, _ := syscall.UTF16PtrFromString(strings.ReplaceAll(title, "\x00", ""))
	messagePointer, _ := syscall.UTF16PtrFromString(strings.ReplaceAll(message, "\x00", ""))
	procMessageBox.Call(0, uintptr(unsafe.Pointer(messagePointer)), uintptr(unsafe.Pointer(titlePointer)), 0x10|0x10000)
}

// LaunchTerminal opens an interactive system-OpenSSH session. Windows Terminal
// is preferred. If unavailable, OpenSSH gets its own native console; this avoids
// PowerShell command interpolation and execution-policy dependencies.
func LaunchTerminal(target string) error {
	return launchTerminal(target, terminalLaunchers{
		lookup:          exec.LookPath,
		windowsTerminal: launchWindowsTerminal,
		console:         launchConsole,
	})
}

type terminalLaunchers struct {
	lookup          func(string) (string, error)
	windowsTerminal func(*exec.Cmd) error
	console         func(string, []string) error
}

func launchTerminal(target string, launchers terminalLaunchers) error {
	if err := validateTerminalTarget(target); err != nil {
		return err
	}
	ssh, err := launchers.lookup("ssh.exe")
	if err != nil {
		return errors.New("OpenSSH Client is not installed or is not on PATH; enable it in Windows Optional features")
	}
	args := []string{"-tt", "--", target}
	var terminalErr error
	if terminal, lookupErr := launchers.lookup("wt.exe"); lookupErr == nil {
		if terminalErr = launchers.windowsTerminal(windowsTerminalCommand(terminal, ssh, target)); terminalErr == nil {
			return nil
		}
		log.Printf("Windows Terminal could not open; using SSH console fallback: %v", terminalErr)
	}
	if err = launchers.console(ssh, args); err != nil && terminalErr != nil {
		return fmt.Errorf("Windows Terminal failed (%v); fallback SSH console failed: %w", terminalErr, err)
	}
	return err
}

func windowsTerminalCommand(terminal, ssh, target string) *exec.Cmd {
	// This is a visible GUI launch. Background() would pass SW_HIDE to Windows
	// Terminal and can leave its window invisible when DGX Hub has no console.
	// A new window also avoids routing the tab to a minimized/hidden existing one.
	return exec.Command(terminal, "-w", "new", "new-tab", "--title", "DGX Hub - "+target, "--", ssh, "-tt", "--", target)
}

type terminalOutput struct{ data []byte }

func (b *terminalOutput) Write(p []byte) (int, error) {
	const limit = 4096
	if len(b.data) < limit {
		b.data = append(b.data, p[:min(len(p), limit-len(b.data))]...)
	}
	return len(p), nil
}

func launchWindowsTerminal(cmd *exec.Cmd) error {
	var output terminalOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start Windows Terminal: %w", err)
	}
	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		// A relay can exit successfully while its GUI child retains output
		// handles. A bounded pipe-drain delay is not an activation failure.
		if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Success() {
			err = nil
		}
		if err != nil {
			detail := strings.TrimSpace(string(output.data))
			if detail != "" {
				err = fmt.Errorf("Windows Terminal exited: %w: %s", err, detail)
			} else {
				err = fmt.Errorf("Windows Terminal exited: %w", err)
			}
		}
		done <- err
	}()
	// The relay launcher often exits immediately. The first Terminal instance
	// can instead own the GUI message loop for the whole interactive session.
	// Observe early failure, but never wait for or kill that interactive window.
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		go func() {
			if err := <-done; err != nil {
				log.Printf("Windows Terminal ended after launch: %v", err)
			}
		}()
		return nil
	}
}

// launchConsole uses CreateProcess directly so a new console receives native
// console handles, rather than inheriting the GUI application's null handles.
// Windows accepts a command line at this boundary; EscapeArg preserves the
// individual arguments exactly. No command interpreter is involved.
func launchConsole(executable string, args []string) error {
	application, err := syscall.UTF16PtrFromString(executable)
	if err != nil {
		return err
	}
	words := make([]string, 0, len(args)+1)
	words = append(words, syscall.EscapeArg(executable))
	for _, arg := range args {
		words = append(words, syscall.EscapeArg(arg))
	}
	commandLine, err := syscall.UTF16PtrFromString(strings.Join(words, " "))
	if err != nil {
		return err
	}
	var startup syscall.StartupInfo
	startup.Cb = uint32(unsafe.Sizeof(startup))
	var process syscall.ProcessInformation
	result, _, callErr := procCreateProcess.Call(
		uintptr(unsafe.Pointer(application)), uintptr(unsafe.Pointer(commandLine)),
		0, 0, 0, createNewConsole, 0, 0,
		uintptr(unsafe.Pointer(&startup)), uintptr(unsafe.Pointer(&process)),
	)
	if result == 0 {
		return fmt.Errorf("open SSH console: %w", callErr)
	}
	_ = syscall.CloseHandle(process.Thread)
	_ = syscall.CloseHandle(process.Process)
	return nil
}
