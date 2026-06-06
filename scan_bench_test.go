package jseek

import (
	"bytes"
	"testing"
)

// These benchmarks quantify the payoff of wider scan lanes, to inform whether
// hand-written SIMD (AVX2/NEON) is worth the maintenance cost over the portable
// SWAR core. They scan a long string body for the terminating quote — the
// dominant hot loop in real JSON.

func makeScanBody(n int) []byte {
	b := bytes.Repeat([]byte("abcdefghij klmno pqrst uvwxyz 0123456789 "), n/41+1)
	b = b[:n]
	b = append(b, '"') // terminator at the very end (worst case: scan all)
	return b
}

var scanInput = makeScanBody(4096)

func naiveIndexQuote(data []byte, i int) int {
	for ; i < len(data); i++ {
		if data[i] == '"' || data[i] == '\\' {
			return i
		}
	}
	return -1
}

func BenchmarkScan_Naive(b *testing.B) {
	b.SetBytes(int64(len(scanInput)))
	for i := 0; i < b.N; i++ {
		_ = naiveIndexQuote(scanInput, 0)
	}
}

func BenchmarkScan_SWAR8(b *testing.B) {
	b.SetBytes(int64(len(scanInput)))
	for i := 0; i < b.N; i++ {
		_ = indexQuoteOrBackslashSWAR(scanInput, 0)
	}
}

func BenchmarkScan_SWAR16(b *testing.B) {
	b.SetBytes(int64(len(scanInput)))
	for i := 0; i < b.N; i++ {
		_ = indexQuoteOrBackslashSWAR16(scanInput, 0)
	}
}
