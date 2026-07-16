//go:build arm64 && !purego

package jseek

func indexStructuralOrQuote(data []byte, i int) int {
	if len(data)-i >= 16 {
		return indexStructuralOrQuoteNEON(data, i)
	}
	return indexStructuralOrQuoteSWAR(data, i)
}
