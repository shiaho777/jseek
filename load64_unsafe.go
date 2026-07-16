//go:build !jseeksafe

package jseek

import "unsafe"

func load64(data []byte, i int) uint64 {
	return *(*uint64)(unsafe.Pointer(&data[i]))
}
