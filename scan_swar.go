package jseek

import (
	"math/bits"
)

const (
	swarLSB = 0x0101010101010101
	swarMSB = 0x8080808080808080
)

func broadcast(c byte) uint64 {
	return uint64(c) * swarLSB
}

func zeroByteMask(v uint64) uint64 {
	return (v - swarLSB) & ^v & swarMSB
}

func quoteBackslashMask(v, quote, back uint64) uint64 {
	return zeroByteMask(v^quote) | zeroByteMask(v^back)
}

func indexQuoteOrBackslashSWAR(data []byte, i int) int {
	n := len(data)
	quote := broadcast('"')
	back := broadcast('\\')

	for i+32 <= n {
		v0 := load64(data, i)
		m0 := quoteBackslashMask(v0, quote, back)
		if m0 != 0 {
			return i + bits.TrailingZeros64(m0)>>3
		}
		v1 := load64(data, i+8)
		m1 := quoteBackslashMask(v1, quote, back)
		if m1 != 0 {
			return i + 8 + bits.TrailingZeros64(m1)>>3
		}
		v2 := load64(data, i+16)
		m2 := quoteBackslashMask(v2, quote, back)
		if m2 != 0 {
			return i + 16 + bits.TrailingZeros64(m2)>>3
		}
		v3 := load64(data, i+24)
		m3 := quoteBackslashMask(v3, quote, back)
		if m3 != 0 {
			return i + 24 + bits.TrailingZeros64(m3)>>3
		}
		i += 32
	}
	for i+8 <= n {
		v := load64(data, i)
		m := quoteBackslashMask(v, quote, back)
		if m != 0 {
			return i + bits.TrailingZeros64(m)>>3
		}
		i += 8
	}
	for i < n {
		c := data[i]
		if c == '"' || c == '\\' {
			return i
		}
		i++
	}
	return -1
}

func indexQuoteOrBackslashSWAR8(data []byte, i int) int {
	n := len(data)
	quote := broadcast('"')
	back := broadcast('\\')
	for i+8 <= n {
		v := load64(data, i)
		m := quoteBackslashMask(v, quote, back)
		if m != 0 {
			return i + bits.TrailingZeros64(m)>>3
		}
		i += 8
	}
	for i < n {
		c := data[i]
		if c == '"' || c == '\\' {
			return i
		}
		i++
	}
	return -1
}

func indexQuoteOrBackslashSWAR16(data []byte, i int) int {
	return indexQuoteOrBackslashSWAR(data, i)
}


func indexSkipWhitespaceSWAR(data []byte, i int) int {
	n := len(data)
	for i+8 <= n {
		v := load64(data, i)
		m := nonWhitespaceMask(v)
		if m != 0 {
			return i + bits.TrailingZeros64(m)>>3
		}
		i += 8
	}
	for i < n {
		switch data[i] {
		case ' ', '\t', '\n', '\r':
			i++
		default:
			return i
		}
	}
	return i
}

func nonWhitespaceMask(v uint64) uint64 {
	isSpace := zeroByteMask(v ^ broadcast(' '))
	isTab := zeroByteMask(v ^ broadcast('\t'))
	isNL := zeroByteMask(v ^ broadcast('\n'))
	isCR := zeroByteMask(v ^ broadcast('\r'))
	ws := isSpace | isTab | isNL | isCR
	return ^ws & swarMSB
}

func structuralOrQuoteMask(v uint64) uint64 {
	m := zeroByteMask(v ^ broadcast('"'))
	m |= zeroByteMask(v ^ broadcast('{'))
	m |= zeroByteMask(v ^ broadcast('}'))
	m |= zeroByteMask(v ^ broadcast('['))
	m |= zeroByteMask(v ^ broadcast(']'))
	m |= zeroByteMask(v ^ broadcast(':'))
	m |= zeroByteMask(v ^ broadcast(','))
	return m
}

func indexStructuralOrQuoteSWAR(data []byte, i int) int {
	n := len(data)
	for i+16 <= n {
		v0 := load64(data, i)
		m0 := structuralOrQuoteMask(v0)
		if m0 != 0 {
			return i + bits.TrailingZeros64(m0)>>3
		}
		v1 := load64(data, i+8)
		m1 := structuralOrQuoteMask(v1)
		if m1 != 0 {
			return i + 8 + bits.TrailingZeros64(m1)>>3
		}
		i += 16
	}
	for i+8 <= n {
		v := load64(data, i)
		m := structuralOrQuoteMask(v)
		if m != 0 {
			return i + bits.TrailingZeros64(m)>>3
		}
		i += 8
	}
	for i < n {
		if structuralKind[data[i]] != kNone {
			return i
		}
		i++
	}
	return -1
}

func containerSpecialMask(v uint64) uint64 {
	m := zeroByteMask(v ^ broadcast('"'))
	m |= zeroByteMask(v ^ broadcast('{'))
	m |= zeroByteMask(v ^ broadcast('}'))
	m |= zeroByteMask(v ^ broadcast('['))
	m |= zeroByteMask(v ^ broadcast(']'))
	return m
}

func indexContainerSpecial(data []byte, i int) int {
	n := len(data)
	for i+16 <= n {
		v0 := load64(data, i)
		m0 := containerSpecialMask(v0)
		if m0 != 0 {
			return i + bits.TrailingZeros64(m0)>>3
		}
		v1 := load64(data, i+8)
		m1 := containerSpecialMask(v1)
		if m1 != 0 {
			return i + 8 + bits.TrailingZeros64(m1)>>3
		}
		i += 16
	}
	for i+8 <= n {
		v := load64(data, i)
		m := containerSpecialMask(v)
		if m != 0 {
			return i + bits.TrailingZeros64(m)>>3
		}
		i += 8
	}
	for i < n {
		c := data[i]
		if c == '"' || c == '{' || c == '}' || c == '[' || c == ']' {
			return i
		}
		i++
	}
	return -1
}
