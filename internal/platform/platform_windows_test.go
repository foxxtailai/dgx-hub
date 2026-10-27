//go:build windows

package platform

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
	"unsafe"
)

func TestWin32StructureLayouts(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("current delivery targets Windows amd64")
	}
	for _, item := range []struct {
		name      string
		got, want uintptr
	}{
		{"WNDCLASSEXW", unsafe.Sizeof(windowClass{}), 80},
		{"MSG", unsafe.Sizeof(windowMessage{}), 48},
		{"NOTIFYICONDATAW", unsafe.Sizeof(notifyIconData{}), 976},
	} {
		if item.got != item.want {
			t.Errorf("%s layout: got %d, want %d", item.name, item.got, item.want)
		}
	}
}

func TestBackgroundUsesHiddenConsoleFlags(t *testing.T) {
	cmd := exec.Command("ssh.exe", "elly")
	Background(cmd)
	if !cmd.SysProcAttr.HideWindow || cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatal("SSH background process can open a visible console")
	}
}

func TestNativeIconResourceLoads(t *testing.T) {
	resource := iconResource()
	icon, _, err := procCreateIcon.Call(uintptr(unsafe.Pointer(&resource[0])), uintptr(len(resource)), 1, 0x00030000, 32, 32, 0)
	if icon == 0 {
		t.Fatalf("native icon could not be decoded: %v", err)
	}
	procDestroyIcon.Call(icon)
}

func TestDataDirUsesWindowsUserData(t *testing.T) {
	base := t.TempDir()
	t.Setenv("LOCALAPPDATA", base)
	got, err := DataDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(base, "DGXHub") {
		t.Fatalf("unexpected data directory %q", got)
	}
	legacy, err := LegacyDatabasePath("")
	if err != nil || legacy != filepath.Join(base, "FoxHub", "foxhub.db") {
		t.Fatalf("unexpected legacy database path %q: %v", legacy, err)
	}
	custom := t.TempDir()
	legacy, err = LegacyDatabasePath(custom)
	if err != nil || legacy != filepath.Join(custom, "foxhub.db") {
		t.Fatalf("unexpected custom legacy path %q: %v", legacy, err)
	}
}

// This opt-in test adds a real tray icon briefly and verifies message-loop
// cancellation and native cleanup on a Windows desktop.
func TestTrayLifecycle(t *testing.T) {
	if os.Getenv("DGX_HUB_TEST_TRAY") != "1" {
		t.Skip("set DGX_HUB_TEST_TRAY=1 on a Windows desktop")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := RunTray(ctx, "http://127.0.0.1:8787/", nil); err != nil {
		t.Fatal(err)
	}
}
