//go:build (amd64 || arm64) && !purego

package jseek

// Native-architecture dispatch seam.
//
// This is where hand-written SIMD kernels hook in: an AVX2/AVX-512 kernel
// (amd64) or a NEON kernel (arm64) would be added as scan_simd_<arch>.s with a
// Go stub, and selected here — typically behind a runtime CPU-feature check set
// in an init function. Until such a kernel lands and passes the differential
// fuzz suite in CI on its architecture, these route to the portable SWAR core,
// which keeps every build correct and fast.
//
// The seam is intentionally a single indirection so that enabling SIMD is a
// localized change with no effect on any caller above the byte-scanning layer.

const simdEnabled = false

func indexQuoteOrBackslash(data []byte, i int) int { return indexQuoteOrBackslashSWAR(data, i) }
func indexSkipWhitespace(data []byte, i int) int   { return indexSkipWhitespaceSWAR(data, i) }
