//go:build !jseeksafe

package jseek

import "unsafe"

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
