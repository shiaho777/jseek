//go:build !unix

package jseek

// isUnsupportedSync on non-unix platforms conservatively treats sync errors as
// unsupported (best-effort durability), since directory fsync semantics vary and
// the atomic rename still guarantees no torn file.
func isUnsupportedSync(err error) bool { return true }
