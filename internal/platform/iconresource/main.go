// Command iconresource generates a Windows/amd64 COFF resource object using
// only the standard library. It embeds DGX Hub's icon, version and manifest.
//
//	go run ./internal/platform/iconresource -out cmd/dgx-hub/resources_windows_amd64.syso -version 0.1.2
package main

import (
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"

	"dgx-hub/internal/platform"
)

func main() {
	output := flag.String("out", "", "output Windows/amd64 .syso file")
	version := flag.String("version", "0.1.2", "application version (major.minor.patch)")
	iconPath := flag.String("icon", "", "optional ICO file; defaults to DGX Hub's application icon")
	flag.Parse()
	if *output == "" {
		fail(errors.New("-out is required"))
	}
	icon := platform.IconICO()
	if *iconPath != "" {
		var err error
		icon, err = os.ReadFile(*iconPath)
		if err != nil {
			fail(err)
		}
	}
	data, err := buildObject(icon, *version)
	if err != nil {
		fail(err)
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		fail(err)
	}
	if err := os.WriteFile(*output, data, 0o644); err != nil {
		fail(err)
	}
}

func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }

type resource struct {
	kind uint32
	id   uint32
	data []byte
}

func buildObject(ico []byte, version string) ([]byte, error) {
	images, group, err := iconResources(ico)
	if err != nil {
		return nil, err
	}
	versionInfo, err := versionResource(version)
	if err != nil {
		return nil, err
	}
	resources := append(images, resource{14, 1, group}, resource{16, 1, versionInfo}, resource{24, 1, []byte(applicationManifest)})
	raw, relocations := resourceSection(resources)
	const headerSize = 20 + 40
	symbolOffset := headerSize + len(raw) + len(relocations)*10
	object := make([]byte, symbolOffset+18+4)
	put16(object, 0, 0x8664) // IMAGE_FILE_MACHINE_AMD64.
	put16(object, 2, 1)
	put32(object, 8, uint32(symbolOffset))
	put32(object, 12, 1)
	copy(object[20:28], ".rsrc")
	put32(object, 20+16, uint32(len(raw)))
	put32(object, 20+20, headerSize)
	put32(object, 20+24, uint32(headerSize+len(raw)))
	put16(object, 20+32, uint16(len(relocations)))
	put32(object, 20+36, 0x40300040) // Initialized, readable, 4-byte aligned.
	copy(object[headerSize:], raw)
	for i, offset := range relocations {
		at := headerSize + len(raw) + i*10
		put32(object, at, offset)
		put32(object, at+4, 0)      // The .rsrc section symbol.
		put16(object, at+8, 0x0003) // IMAGE_REL_AMD64_ADDR32NB.
	}
	copy(object[symbolOffset:symbolOffset+8], ".rsrc")
	put16(object, symbolOffset+12, 1)
	object[symbolOffset+16] = 3       // IMAGE_SYM_CLASS_STATIC.
	put32(object, symbolOffset+18, 4) // Empty COFF string table.
	return object, nil
}

func iconResources(ico []byte) ([]resource, []byte, error) {
	if len(ico) < 22 || binary.LittleEndian.Uint16(ico[0:2]) != 0 || binary.LittleEndian.Uint16(ico[2:4]) != 1 {
		return nil, nil, errors.New("invalid ICO header")
	}
	count := int(binary.LittleEndian.Uint16(ico[4:6]))
	if count < 1 || count > (len(ico)-6)/16 {
		return nil, nil, errors.New("invalid ICO directory")
	}
	images := make([]resource, 0, count)
	group := make([]byte, 6+14*count)
	put16(group, 2, 1)
	put16(group, 4, uint16(count))
	for i := 0; i < count; i++ {
		at := 6 + i*16
		entry := ico[at : at+16]
		size := uint64(binary.LittleEndian.Uint32(entry[8:12]))
		offset := uint64(binary.LittleEndian.Uint32(entry[12:16]))
		if size == 0 || offset < uint64(6+count*16) || offset+size > uint64(len(ico)) {
			return nil, nil, errors.New("invalid ICO image bounds")
		}
		images = append(images, resource{3, uint32(i + 1), ico[offset : offset+size]})
		groupAt := 6 + i*14
		copy(group[groupAt:groupAt+12], entry[:12])
		put16(group, groupAt+12, uint16(i+1))
	}
	return images, group, nil
}

func resourceSection(resources []resource) ([]byte, []uint32) {
	// Three sorted numeric directory levels: resource type, image ID, en-US.
	// Every icon size has its own RT_ICON entry under the same resource type.
	resources = slices.Clone(resources)
	slices.SortFunc(resources, func(a, b resource) int {
		if a.kind != b.kind {
			return int(a.kind) - int(b.kind)
		}
		return int(a.id) - int(b.id)
	})
	var kinds []uint32
	for _, item := range resources {
		if len(kinds) == 0 || kinds[len(kinds)-1] != item.kind {
			kinds = append(kinds, item.kind)
		}
	}
	data := make([]byte, 16+8*len(kinds))
	put16(data, 14, uint16(len(kinds)))
	entries := make([]int, len(resources))
	index := 0
	for typeIndex, kind := range kinds {
		start := index
		for index < len(resources) && resources[index].kind == kind {
			index++
		}
		count := index - start
		typeOffset := len(data)
		data = append(data, make([]byte, 16+8*count)...)
		put32(data, 16+typeIndex*8, kind)
		put32(data, 20+typeIndex*8, uint32(typeOffset)|0x80000000)
		put16(data, typeOffset+14, uint16(count))
		for i := start; i < index; i++ {
			languageOffset := len(data)
			data = append(data, make([]byte, 24+16)...)
			dataOffset := languageOffset + 24
			nameAt := typeOffset + 16 + (i-start)*8
			put32(data, nameAt, resources[i].id)
			put32(data, nameAt+4, uint32(languageOffset)|0x80000000)
			put16(data, languageOffset+14, 1)
			put32(data, languageOffset+16, 0x0409)
			put32(data, languageOffset+20, uint32(dataOffset))
			entries[i] = dataOffset
		}
	}
	relocations := make([]uint32, len(resources))
	for i, item := range resources {
		for len(data)%4 != 0 {
			data = append(data, 0)
		}
		put32(data, entries[i], uint32(len(data)))
		put32(data, entries[i]+4, uint32(len(item.data)))
		relocations[i] = uint32(entries[i])
		data = append(data, item.data...)
	}
	return data, relocations
}

func versionResource(version string) ([]byte, error) {
	parts := strings.Split(strings.SplitN(strings.TrimPrefix(version, "v"), "-", 2)[0], ".")
	if len(parts) != 3 {
		return nil, errors.New("version must have major.minor.patch format")
	}
	numbers := make([]uint32, 3)
	for i, part := range parts {
		number, err := strconv.ParseUint(part, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("invalid version %q", version)
		}
		numbers[i] = uint32(number)
	}
	fixed := make([]byte, 52)
	for i, value := range []uint32{0xFEEF04BD, 0x00010000, numbers[0]<<16 | numbers[1], numbers[2] << 16, numbers[0]<<16 | numbers[1], numbers[2] << 16, 0x3f, 0, 0x00040004, 1, 0, 0, 0} {
		put32(fixed, i*4, value)
	}
	var stringsInfo [][]byte
	for _, pair := range [][2]string{
		{"FileDescription", "DGX Hub local SSH dashboard"}, {"FileVersion", version},
		{"InternalName", "DGXHub"}, {"OriginalFilename", "dgx-hub.exe"},
		{"ProductName", "DGX Hub"}, {"ProductVersion", version},
	} {
		value := wide(pair[1])
		stringsInfo = append(stringsInfo, versionBlock(pair[0], value, uint16(len(value)/2), 1))
	}
	stringTable := versionBlock("040904b0", nil, 0, 1, stringsInfo...)
	stringFileInfo := versionBlock("StringFileInfo", nil, 0, 1, stringTable)
	translation := []byte{0x09, 0x04, 0xb0, 0x04}
	varFileInfo := versionBlock("VarFileInfo", nil, 0, 1, versionBlock("Translation", translation, 4, 0))
	return versionBlock("VS_VERSION_INFO", fixed, uint16(len(fixed)), 0, stringFileInfo, varFileInfo), nil
}

func versionBlock(key string, value []byte, valueLength, kind uint16, children ...[]byte) []byte {
	data := make([]byte, 6)
	put16(data, 2, valueLength)
	put16(data, 4, kind)
	data = append(data, wide(key)...)
	for len(data)%4 != 0 {
		data = append(data, 0)
	}
	data = append(data, value...)
	for _, child := range children {
		for len(data)%4 != 0 {
			data = append(data, 0)
		}
		data = append(data, child...)
	}
	put16(data, 0, uint16(len(data)))
	return data
}

func wide(value string) []byte {
	units := utf16.Encode([]rune(value + "\x00"))
	result := make([]byte, len(units)*2)
	for i, unit := range units {
		put16(result, i*2, unit)
	}
	return result
}

func put16(data []byte, offset int, value uint16) {
	binary.LittleEndian.PutUint16(data[offset:offset+2], value)
}
func put32(data []byte, offset int, value uint32) {
	binary.LittleEndian.PutUint32(data[offset:offset+4], value)
}

const applicationManifest = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <assemblyIdentity version="1.0.0.0" processorArchitecture="amd64" name="DGXHub" type="win32"/>
  <description>DGX Hub local SSH dashboard</description>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3"><security><requestedPrivileges><requestedExecutionLevel level="asInvoker" uiAccess="false"/></requestedPrivileges></security></trustInfo>
  <compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1"><application><supportedOS Id="{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}"/></application></compatibility>
  <application xmlns="urn:schemas-microsoft-com:asm.v3"><windowsSettings><dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">PerMonitorV2</dpiAwareness></windowsSettings></application>
</assembly>`
