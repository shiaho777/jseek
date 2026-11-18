//go:build unix

package jseek

import (
	"errors"
	"syscall"
)

// isUnsupportedSync reports whether err means the platform/filesystem does not
// support fsync on this descriptor (e.g. a directory on some mounts). EINVAL and
// ENOTSUP/EOPNOTSUPP are treated as best-effort tolerated; other errors propagate.
func isUnsupportedSync(err error) bool {
	return errors.Is(err, syscall.EINVAL) ||
		errors.Is(err, syscall.ENOTSUP) ||
		errors.Is(err, syscall.EOPNOTSUPP)
}
