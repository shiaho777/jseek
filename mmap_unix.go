//go:build unix

package jseek

import (
	"os"
	"syscall"
)

// mmapReadOnly maps f's first size bytes read-only. The mapping aliases the
// file; the caller must keep f open and the file immutable for its lifetime.
func mmapReadOnly(f *os.File, size int) ([]byte, error) {
	if size == 0 {
		return nil, ErrBadIndex
	}
	b, err := syscall.Mmap(int(f.Fd()), 0, size, syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func munmap(b []byte) {
	_ = syscall.Munmap(b)
}