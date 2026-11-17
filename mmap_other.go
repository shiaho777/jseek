//go:build !unix

package jseek

import "os"

// mmapReadOnly on non-unix platforms falls back to reading the file into
// memory (no true mapping). Semantics match: the returned bytes are immutable
// for the Store's lifetime and released by munmap (a no-op here).
func mmapReadOnly(f *os.File, size int) ([]byte, error) {
	if size == 0 {
		return nil, ErrBadIndex
	}
	b := make([]byte, size)
	if _, err := f.ReadAt(b, 0); err != nil {
		return nil, err
	}
	return b, nil
}

func munmap(b []byte) {}