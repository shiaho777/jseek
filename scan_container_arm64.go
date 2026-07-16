//go:build arm64 && !purego

package jseek

func skipContainer(data []byte, i int) (int, bool) {
	return skipContainerNEON(data, i)
}

//go:noescape
func skipContainerNEON(data []byte, i int) (int, bool)
