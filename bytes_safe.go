//go:build jseeksafe

package jseek

import "encoding/binary"

// safeBuild reports whether this is the allocating, no-unsafe build.
const safeBuild = true

// btosUnsafe in the "jseeksafe" build allocates a real copy, trading the speed of
// the zero-copy view for absolute memory safety. Enable with -tags jseeksafe.
func btosUnsafe(b []byte) string {
	return string(b)
}

// u32Slice in the "jseeksafe" build decodes into a fresh []uint32 (no unsafe
// pointer reinterpretation), preserving absolute memory safety at the cost of
// one allocation. Enable with -tags jseeksafe.
func u32Slice(b []byte) []uint32 {
	if len(b) == 0 {
		return nil
	}
	s := make([]uint32, len(b)/4)
	for i := range s {
		s[i] = binary.LittleEndian.Uint32(b[i*4:])
	}
	return s
}
