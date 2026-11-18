//go:build unix

package jseek

import (
	"os"
	"syscall"
)

// fileLock is an advisory exclusive lock on a sidecar file, used to coordinate
// writers across processes. flock is released automatically by the kernel if the
// holding process dies, so a crashed writer never leaves a stale lock.
type fileLock struct {
	f *os.File
}

// acquireLock opens (creating if needed) the lock file at path and takes a
// non-blocking exclusive lock. It returns ErrLocked if another process holds it.
func acquireLock(path string) (*fileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, ErrLocked
		}
		return nil, err
	}
	return &fileLock{f: f}, nil
}

// release drops the lock and closes the file.
func (l *fileLock) release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	cerr := l.f.Close()
	l.f = nil
	if err != nil {
		return err
	}
	return cerr
}
