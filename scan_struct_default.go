//go:build !arm64 || purego

package jseek

func indexStructuralOrQuote(data []byte, i int) int {
	return indexStructuralOrQuoteSWAR(data, i)
}
