package main

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"testing"

	"dgx-hub/internal/platform"
)

func TestResourceObjectIsValidAMD64COFF(t *testing.T) {
	data, err := buildObject(platform.IconICO(), "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	object, err := pe.NewFile(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("invalid COFF object: %v", err)
	}
	defer object.Close()
	if object.Machine != pe.IMAGE_FILE_MACHINE_AMD64 || len(object.Sections) != 1 || object.Sections[0].Name != ".rsrc" {
		t.Fatal("missing amd64 resource section")
	}
	section := object.Sections[0]
	if len(section.Relocs) != 12 {
		t.Fatalf("expected nine icon sizes and three metadata resources, got %d", len(section.Relocs))
	}
	raw, err := section.Data()
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(raw[14:16]) != 4 {
		t.Fatal("expected icon, icon-group, version and manifest resources")
	}
	for _, relocation := range section.Relocs {
		if relocation.Type != 3 || int(relocation.VirtualAddress)+16 > len(raw) {
			t.Fatal("invalid resource relocation")
		}
		offset := binary.LittleEndian.Uint32(raw[relocation.VirtualAddress : relocation.VirtualAddress+4])
		size := binary.LittleEndian.Uint32(raw[relocation.VirtualAddress+4 : relocation.VirtualAddress+8])
		if uint64(offset)+uint64(size) > uint64(len(raw)) {
			t.Fatal("resource payload outside section")
		}
	}
}

func TestICOGroupReferencesEverySize(t *testing.T) {
	ico := platform.IconICO()
	images, group, err := iconResources(ico)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 9 || binary.LittleEndian.Uint16(group[4:6]) != 9 {
		t.Fatal("icon sizes were dropped from the group")
	}
	for i, image := range images {
		entry := group[6+i*14 : 20+i*14]
		if image.kind != 3 || uint32(binary.LittleEndian.Uint16(entry[12:14])) != image.id || int(binary.LittleEndian.Uint32(entry[8:12])) != len(image.data) {
			t.Fatalf("group references wrong image at %d", i)
		}
	}
	// Bounds checking must cover the largest image too, not just the tray size.
	put32(ico, 6+8*16+12, 0xffffffff)
	if _, _, err := iconResources(ico); err == nil {
		t.Fatal("accepted an out-of-bounds large icon")
	}
}

func TestRejectMalformedICOAndVersion(t *testing.T) {
	if _, _, err := iconResources([]byte("not an icon")); err == nil {
		t.Fatal("accepted malformed ICO")
	}
	icon := platform.IconICO()
	put32(icon, 18, 0xffffffff)
	if _, _, err := iconResources(icon); err == nil {
		t.Fatal("accepted out-of-bounds ICO image")
	}
	for _, version := range []string{"1", "1.2", "1.2.999999", "abc.2.3"} {
		if _, err := versionResource(version); err == nil {
			t.Errorf("accepted malformed version %q", version)
		}
	}
}
