//go:build jseeksafe

package jseek

// btosUnsafe in the "jseeksafe" build allocates a real copy, trading the speed of
// the zero-copy view for absolute memory safety. Enable with -tags jseeksafe.
func btosUnsafe(b []byte) string {
	return string(b)
}
