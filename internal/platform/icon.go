package platform

import (
	_ "embed"
	"encoding/binary"
	"slices"
)

// applicationIcon contains the supplied DGX Hub artwork at sizes 16–256.
// Regenerate it with scripts/update-icons.ps1 after replacing assets/app-icon.png.
//
//go:embed assets/app.ico
var applicationIcon []byte

// iconResource returns the 32-bit DIB used by the native Windows tray.
func iconResource() []byte {
	count := int(binary.LittleEndian.Uint16(applicationIcon[4:6]))
	for i := 0; i < count; i++ {
		entry := applicationIcon[6+i*16 : 22+i*16]
		if entry[0] == 32 && entry[1] == 32 {
			size := binary.LittleEndian.Uint32(entry[8:12])
			offset := binary.LittleEndian.Uint32(entry[12:16])
			return slices.Clone(applicationIcon[offset : offset+size])
		}
	}
	panic("application icon is missing its 32px tray image")
}

// IconICO exposes a copy of all icon sizes for Windows resource generation.
func IconICO() []byte { return slices.Clone(applicationIcon) }
