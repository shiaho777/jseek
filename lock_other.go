//go:build !unix

package jseek

// On non-unix platforms advisory cross-process flock is not available. The
// single-writer guarantee still holds within a process (WALStore's mutex); for
// cross-process coordination we conservatively report the store as locked so a
// second writer fails fast rather than risking interleaved corruption. Callers
// on these platforms should ensure a single writer externally.
type fileLock struct{}

func acquireLock(path string) (*fileLock, error) {
	return nil, ErrLocked
}

func (l *fileLock) release() error { return nil }
