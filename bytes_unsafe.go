//go:build !jseeksafe

package jseek

import "unsafe"

// safeBuild reports whether this is the allocating, no-unsafe build.
const safeBuild = false

// btosUnsafe returns a string that shares storage with b, performing no
// allocation. The caller must guarantee b is not mutated for the lifetime of
// the returned string. Built by default; use the "jseeksafe" build tag to get the
// allocating, fully safe implementation in bytes_safe.go.
func btosUnsafe(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(unsafe.SliceData(b), len(b))
}

// u32Slice reinterprets b (len multiple of 4, 4-byte aligned) as a []uint32
// sharing storage, with no allocation or copy. Used by View to map a
// persisted index directly. The caller guarantees b's lifetime and alignment.
func u32Slice(b []byte) []uint32 {
	if len(b) == 0 {
		return nil
	}
	return unsafe.Slice((*uint32)(unsafe.Pointer(unsafe.SliceData(b))), len(b)/4)
}
