//go:build !arm64 || purego

package jseek

func skipContainer(data []byte, i int) (int, bool) {
	return skipContainerGeneric(data, i)
}
