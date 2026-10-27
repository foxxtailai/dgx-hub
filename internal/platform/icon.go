package platform

import "encoding/binary"

// iconResource is a native 32-bit DIB icon: a green plus on a dark square.
// Its mask and alpha channel are both populated, including on Windows 10.
func iconResource() []byte {
	const size = 32
	const pixels = size * size * 4
	const mask = size * size / 8
	icon := make([]byte, 40+pixels+mask)
	binary.LittleEndian.PutUint32(icon[0:4], 40)
	binary.LittleEndian.PutUint32(icon[4:8], size)
	binary.LittleEndian.PutUint32(icon[8:12], size*2)
	binary.LittleEndian.PutUint16(icon[12:14], 1)
	binary.LittleEndian.PutUint16(icon[14:16], 32)
	binary.LittleEndian.PutUint32(icon[20:24], pixels)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			i := 40 + (y*size+x)*4
			// DIB pixels are BGRA and bottom-up. The design is symmetric.
			icon[i], icon[i+1], icon[i+2], icon[i+3] = 28, 24, 20, 255
			if (x >= 13 && x <= 18 && y >= 7 && y <= 24) || (y >= 13 && y <= 18 && x >= 7 && x <= 24) {
				icon[i], icon[i+1], icon[i+2] = 128, 222, 83
			}
		}
	}
	return icon
}

// IconICO exposes the same artwork as a standard .ico file for build tooling.
func IconICO() []byte {
	resource := iconResource()
	result := make([]byte, 22+len(resource))
	binary.LittleEndian.PutUint16(result[2:4], 1)
	binary.LittleEndian.PutUint16(result[4:6], 1)
	result[6], result[7] = 32, 32
	binary.LittleEndian.PutUint16(result[10:12], 1)
	binary.LittleEndian.PutUint16(result[12:14], 32)
	binary.LittleEndian.PutUint32(result[14:18], uint32(len(resource)))
	binary.LittleEndian.PutUint32(result[18:22], 22)
	copy(result[22:], resource)
	return result
}
