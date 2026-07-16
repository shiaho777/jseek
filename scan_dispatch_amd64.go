//go:build amd64 && !purego

package jseek

const simdEnabled = true

func indexQuoteOrBackslash(data []byte, i int) int {
	if len(data)-i >= 32 {
		return indexQuoteOrBackslashAVX2(data, i)
	}
	return indexQuoteOrBackslashSWAR(data, i)
}

func indexSkipWhitespace(data []byte, i int) int {
	return indexSkipWhitespaceSWAR(data, i)
}
