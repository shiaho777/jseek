//go:build amd64 && !purego

package jseek

//go:noescape
func indexQuoteOrBackslashAVX2(data []byte, i int) int

//go:noescape
func skipStringBodyAVX2(data []byte, i int) (int, bool)
