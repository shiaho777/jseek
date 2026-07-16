//go:build arm64 && !purego

package jseek

import "testing"

func BenchmarkScan_NEON(b *testing.B) {
	b.SetBytes(int64(len(scanInput)))
	for i := 0; i < b.N; i++ {
		_ = indexQuoteOrBackslashNEON(scanInput, 0)
	}
}

func BenchmarkScan_Dispatch(b *testing.B) {
	b.SetBytes(int64(len(scanInput)))
	for i := 0; i < b.N; i++ {
		_ = indexQuoteOrBackslash(scanInput, 0)
	}
}

func BenchmarkSkipStringBody_NEON(b *testing.B) {
	// scanInput ends with quote; body is content before final quote
	body := scanInput
	b.SetBytes(int64(len(body)))
	for i := 0; i < b.N; i++ {
		_, _ = skipStringBodyNEON(body, 0)
	}
}

func BenchmarkSkipStringBody_Dispatch(b *testing.B) {
	body := scanInput
	b.SetBytes(int64(len(body)))
	for i := 0; i < b.N; i++ {
		_, _ = skipStringBody(body, 0)
	}
}
