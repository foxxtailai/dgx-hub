package platform

import (
	"encoding/binary"
	"testing"
)

func TestTerminalTargetRejectsCommandAndOptionInjection(t *testing.T) {
	for _, target := range []string{"", "-oProxyCommand=evil", "elly;calc", "elly & calc", "elly\nwhoami", "$(whoami)", "user@host/evil"} {
		if err := validateTerminalTarget(target); err == nil {
			t.Errorf("accepted unsafe target %q", target)
		}
	}
	for _, target := range []string{"elly", "evie.local", "user@192.168.1.2", "user@[::1]", "::1", "[::1]", "user@fe80::1%eth0"} {
		if err := validateTerminalTarget(target); err != nil {
			t.Errorf("rejected valid target %q: %v", target, err)
		}
	}
}

func TestBrowserOnlyOpensWebAddresses(t *testing.T) {
	for _, address := range []string{"file:///C:/evil.exe", "javascript:alert(1)", "http://user:pass@localhost/", "http://localhost/\n"} {
		if err := validateBrowserURL(address); err == nil {
			t.Errorf("accepted unsafe URL %q", address)
		}
	}
	if err := validateBrowserURL("http://127.0.0.1:8787/"); err != nil {
		t.Fatal(err)
	}
}

func TestICOContainsNativeResource(t *testing.T) {
	icon := IconICO()
	if binary.LittleEndian.Uint16(icon[2:4]) != 1 || binary.LittleEndian.Uint16(icon[4:6]) != 9 {
		t.Fatal("invalid ICO directory")
	}
	for i, size := range []int{16, 20, 24, 32, 40, 48, 64, 128, 256} {
		entry := icon[6+i*16 : 22+i*16]
		if int(entry[0]) != size%256 || int(entry[1]) != size%256 {
			t.Fatalf("missing icon size %d", size)
		}
		offset := binary.LittleEndian.Uint32(entry[12:16])
		length := binary.LittleEndian.Uint32(entry[8:12])
		if length == 0 || uint64(offset)+uint64(length) > uint64(len(icon)) {
			t.Fatal("ICO resource size mismatch")
		}
	}
	if binary.LittleEndian.Uint32(iconResource()[:4]) != 40 {
		t.Fatal("missing native tray DIB resource")
	}
}
