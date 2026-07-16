//go:build jseeksafe

package jseek

import "encoding/binary"

func load64(data []byte, i int) uint64 {
	return binary.LittleEndian.Uint64(data[i:])
}
