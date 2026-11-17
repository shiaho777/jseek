//go:build jseeksafe

package jseek

import "encoding/binary"

func load64(data []byte, i int) uint64 {
	return binary.LittleEndian.Uint64(data[i:])
}

// load64str loads eight bytes of s at offset i. The caller guarantees
// i+8 <= len(s). Written as byte assembly (which the compiler coalesces into
// a single load) so the safe build never converts the string to a slice.
func load64str(s string, i int) uint64 {
	return uint64(s[i]) | uint64(s[i+1])<<8 | uint64(s[i+2])<<16 | uint64(s[i+3])<<24 |
		uint64(s[i+4])<<32 | uint64(s[i+5])<<40 | uint64(s[i+6])<<48 | uint64(s[i+7])<<56
}
