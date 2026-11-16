//go:build !jseeksafe

package jseek

import "unsafe"

func load64(data []byte, i int) uint64 {
	return *(*uint64)(unsafe.Pointer(&data[i]))
}

// load64str loads eight bytes of s at offset i. The caller guarantees
// i+8 <= len(s).
func load64str(s string, i int) uint64 {
	return *(*uint64)(unsafe.Pointer(unsafe.StringData(s[i:])))
}
