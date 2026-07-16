//go:build !arm64 || purego

package jseek

func findIndex(data []byte, ai int, n int) (int, bool) {
	return findIndexGeneric(data, ai, n)
}
