//go:build purego || (!amd64 && !arm64)

package jseek

// Portable dispatch: every architecture without a hand-written SIMD kernel (or
// any build with the "purego" tag) routes the byte-scanning hot loops to the
// SWAR implementations. This is the default on platforms where jseek has no
// assembly, and the guaranteed-correct fallback everywhere.

// simdEnabled reports whether a hardware-SIMD scanner is active. Exposed for
// tests and diagnostics.
const simdEnabled = false

func indexQuoteOrBackslash(data []byte, i int) int { return indexQuoteOrBackslashSWAR(data, i) }
func indexSkipWhitespace(data []byte, i int) int   { return indexSkipWhitespaceSWAR(data, i) }
