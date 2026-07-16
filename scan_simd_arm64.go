//go:build arm64 && !purego

package jseek

//go:noescape
func indexQuoteOrBackslashNEON(data []byte, i int) int

//go:noescape
func skipStringBodyNEON(data []byte, i int) (int, bool)

//go:noescape
func indexStructuralOrQuoteNEON(data []byte, i int) int
