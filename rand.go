package gibberish

import (
	"math/rand/v2"
	"unsafe"
)

// pcgFillBytes fills the byte slice with random bytes from the given PCG generator.
func pcgFillBytes(p *rand.PCG, b []byte) {
	for len(b) >= 8 {
		*(*uint64)(unsafe.Pointer(&b[0])) = p.Uint64()
		b = b[8:]
	}

	if len(b) > 0 {
		r := p.Uint64()

		if len(b) >= 4 {
			*(*uint32)(unsafe.Pointer(&b[0])) = uint32(r)
			b = b[4:]
			r >>= 32
		}

		if len(b) >= 2 {
			*(*uint16)(unsafe.Pointer(&b[0])) = uint16(r)
			b = b[2:]
			r >>= 16
		}

		if len(b) > 0 {
			b[0] = uint8(r)
		}
	}
}
