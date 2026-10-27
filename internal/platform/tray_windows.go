//go:build windows

package platform

import (
	"context"
	"fmt"
	"log"
	"os"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

const (
	wmDestroy     = 0x0002
	wmClose       = 0x0010
	wmNull        = 0x0000
	wmContextMenu = 0x007b
	wmLButtonUp   = 0x0202
	wmRButtonUp   = 0x0205
	wmTray        = 0x8001
	ninSelect     = 0x0400
	ninKeySelect  = 0x0401
	nimAdd        = 0
	nimDelete     = 2
	nimSetVersion = 4
	nifMessage    = 1
	nifIcon       = 2
	nifTip        = 4
	nifShowTip    = 0x80
	menuOpen      = 1
	menuExit      = 2
)

var (
	user32                    = syscall.NewLazyDLL("user32.dll")
	procRegisterClass         = user32.NewProc("RegisterClassExW")
	procUnregisterClass       = user32.NewProc("UnregisterClassW")
	procCreateWindow          = user32.NewProc("CreateWindowExW")
	procDestroyWindow         = user32.NewProc("DestroyWindow")
	procDefWindowProc         = user32.NewProc("DefWindowProcW")
	procGetMessage            = user32.NewProc("GetMessageW")
	procTranslateMessage      = user32.NewProc("TranslateMessage")
	procDispatchMessage       = user32.NewProc("DispatchMessageW")
	procPostMessage           = user32.NewProc("PostMessageW")
	procPostQuitMessage       = user32.NewProc("PostQuitMessage")
	procRegisterWindowMessage = user32.NewProc("RegisterWindowMessageW")
	procCreateIcon            = user32.NewProc("CreateIconFromResourceEx")
	procDestroyIcon           = user32.NewProc("DestroyIcon")
	procCreatePopupMenu       = user32.NewProc("CreatePopupMenu")
	procDestroyMenu           = user32.NewProc("DestroyMenu")
	procAppendMenu            = user32.NewProc("AppendMenuW")
	procGetCursorPos          = user32.NewProc("GetCursorPos")
	procSetForegroundWindow   = user32.NewProc("SetForegroundWindow")
	procTrackPopupMenu        = user32.NewProc("TrackPopupMenu")
	procNotifyIcon            = shell32.NewProc("Shell_NotifyIconW")
	procGetModuleHandle       = kernel32.NewProc("GetModuleHandleW")
)

type point struct{ X, Y int32 }

type windowClass struct {
	Size        uint32
	Style       uint32
	WndProc     uintptr
	ClassExtra  int32
	WindowExtra int32
	Instance    uintptr
	Icon        uintptr
	Cursor      uintptr
	Background  uintptr
	MenuName    *uint16
	ClassName   *uint16
	SmallIcon   uintptr
}

type windowMessage struct {
	Window  uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Point   point
	Private uint32
}

type notifyIconData struct {
	Size            uint32
	Window          uintptr
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Version         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GUID            [16]byte
	BalloonIcon     uintptr
}

// RunTray owns a hidden top-level window and its Win32 message loop. It must
// remain on one OS thread. onExit should return promptly (normally context.CancelFunc).
func RunTray(ctx context.Context, rawURL string, onExit func()) error {
	if err := validateBrowserURL(rawURL); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return nil
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	module, _, _ := procGetModuleHandle.Call(0)
	resource := iconResource()
	icon, _, iconErr := procCreateIcon.Call(uintptr(unsafe.Pointer(&resource[0])), uintptr(len(resource)), 1, 0x00030000, 32, 32, 0)
	if icon == 0 {
		return fmt.Errorf("create tray icon: %w", iconErr)
	}
	defer procDestroyIcon.Call(icon)
	className, _ := syscall.UTF16PtrFromString(fmt.Sprintf("DGXHub.Tray.%d.%d", os.Getpid(), time.Now().UnixNano()))
	taskbarMessageName, _ := syscall.UTF16PtrFromString("TaskbarCreated")
	taskbarCreated, _, _ := procRegisterWindowMessage.Call(uintptr(unsafe.Pointer(taskbarMessageName)))
	var notification notifyIconData
	notification.Size = uint32(unsafe.Sizeof(notification))
	notification.ID = 1
	notification.Flags = nifMessage | nifIcon | nifTip | nifShowTip
	notification.CallbackMessage = wmTray
	notification.Icon = icon
	copy(notification.Tip[:], syscall.StringToUTF16("DGX Hub"))
	notification.Version = 4
	addIcon := func() error {
		result, _, callErr := procNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&notification)))
		if result == 0 {
			return fmt.Errorf("add system tray icon: %w", callErr)
		}
		procNotifyIcon.Call(nimSetVersion, uintptr(unsafe.Pointer(&notification)))
		return nil
	}
	callback := syscall.NewCallback(func(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
		if taskbarCreated != 0 && message == uint32(taskbarCreated) {
			if err := addIcon(); err != nil {
				log.Printf("restore tray icon after Explorer restart: %v", err)
			}
			return 0
		}
		switch message {
		case wmTray:
			event := uint32(lParam & 0xffff)
			if event == wmRButtonUp || event == wmLButtonUp || event == wmContextMenu || event == ninSelect || event == ninKeySelect {
				switch showTrayMenu(hwnd) {
				case menuOpen:
					go func() {
						if err := OpenBrowser(rawURL); err != nil {
							log.Printf("open DGX Hub: %v", err)
						}
					}()
				case menuExit:
					if onExit != nil {
						onExit()
					}
					procPostMessage.Call(hwnd, wmClose, 0, 0)
				}
			}
			return 0
		case wmClose:
			procDestroyWindow.Call(hwnd)
			return 0
		case wmDestroy:
			procNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&notification)))
			procPostQuitMessage.Call(0)
			return 0
		}
		result, _, _ := procDefWindowProc.Call(hwnd, uintptr(message), wParam, lParam)
		return result
	})
	class := windowClass{Size: uint32(unsafe.Sizeof(windowClass{})), WndProc: callback, Instance: module, Icon: icon, ClassName: className, SmallIcon: icon}
	registered, _, registerErr := procRegisterClass.Call(uintptr(unsafe.Pointer(&class)))
	if registered == 0 {
		return fmt.Errorf("register tray window: %w", registerErr)
	}
	defer procUnregisterClass.Call(uintptr(unsafe.Pointer(className)), module)
	hwnd, _, createErr := procCreateWindow.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(className)), 0, 0, 0, 0, 0, 0, 0, module, 0)
	if hwnd == 0 {
		return fmt.Errorf("create hidden tray window: %w", createErr)
	}
	notification.Window = hwnd
	if err := addIcon(); err != nil {
		procDestroyWindow.Call(hwnd)
		return err
	}
	defer procNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&notification)))
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			procPostMessage.Call(hwnd, wmClose, 0, 0)
		case <-done:
		}
	}()
	var message windowMessage
	for {
		result, _, callErr := procGetMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(result) == -1 {
			procDestroyWindow.Call(hwnd)
			return fmt.Errorf("read tray event: %w", callErr)
		}
		if result == 0 {
			return nil
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&message)))
	}
}

func showTrayMenu(hwnd uintptr) uintptr {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return 0
	}
	defer procDestroyMenu.Call(menu)
	openLabel, _ := syscall.UTF16PtrFromString("Open DGX Hub")
	exitLabel, _ := syscall.UTF16PtrFromString("Exit")
	procAppendMenu.Call(menu, 0, menuOpen, uintptr(unsafe.Pointer(openLabel)))
	procAppendMenu.Call(menu, 0x0800, 0, 0)
	procAppendMenu.Call(menu, 0, menuExit, uintptr(unsafe.Pointer(exitLabel)))
	var cursor point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&cursor)))
	procSetForegroundWindow.Call(hwnd)
	// TPM_RETURNCMD | TPM_RIGHTBUTTON. Signed coordinates are widened for Win32.
	selected, _, _ := procTrackPopupMenu.Call(menu, 0x0100|0x0002, uintptr(int64(cursor.X)), uintptr(int64(cursor.Y)), 0, hwnd, 0)
	procPostMessage.Call(hwnd, wmNull, 0, 0)
	return selected
}
