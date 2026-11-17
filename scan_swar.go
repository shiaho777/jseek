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

// zeroByteMask marks candidate zero bytes in v with the classic SWAR trick.
// It never misses a true zero byte, but its borrow chain can over-report
// non-zero bytes (0x01 above a zero passes the borrow through; 0x80 catches
// it on the high bit — even in words with no zero at all). Callers that
// re-check the located byte (quote/backslash/structural scanners) are safe
// with the raw mask and keep its short dependency chain; callers that trust
// the mask directly (whitespace, wide-key terminators) must use
// zeroByteMaskExact instead.
func zeroByteMask(v uint64) uint64 {
	return (v - swarLSB) & ^v & swarMSB
}

// zeroByteMaskExact marks the high bit of every zero byte in v with no false
// positives. The classic trick's borrow chain can flag non-zero bytes even
// in words containing no zero at all (any 0x80 byte above a byte < 0x01,
// common with UTF-8 text), so masks that are trusted without a per-byte
// re-check must use this variant.
//
// The classic mask is a superset of the true zeros, so when it is 0 the word
// has no zero byte and we return immediately — the common case inside string
// bodies pays only the short classic chain. Otherwise the exact formula
// ^(v | ((v | MSB) - LSB)) & MSB refines it; its borrow can only clear a high
// bit that v already sets, so it never creates a false mark.
func zeroByteMaskExact(v uint64) uint64 {
	if (v-swarLSB)&^v&swarMSB == 0 {
		return 0
	}
	return ^(v | ((v | swarMSB) - swarLSB)) & swarMSB
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
	isSpace := zeroByteMaskExact(v ^ broadcast(' '))
	isTab := zeroByteMaskExact(v ^ broadcast('\t'))
	isNL := zeroByteMaskExact(v ^ broadcast('\n'))
	isCR := zeroByteMaskExact(v ^ broadcast('\r'))
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
	for i+32 <= n {
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
		v2 := load64(data, i+16)
		m2 := structuralOrQuoteMask(v2)
		if m2 != 0 {
			return i + 16 + bits.TrailingZeros64(m2)>>3
		}
		v3 := load64(data, i+24)
		m3 := structuralOrQuoteMask(v3)
		if m3 != 0 {
			return i + 24 + bits.TrailingZeros64(m3)>>3
		}
		i += 32
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

func skipStringShort(data []byte, i int) int {
	n := len(data)
	body := i + 1
	if body >= n {
		return -1
	}
	if body+8 <= n {
		v := load64(data, body)
		quote := broadcast('"')
		back := broadcast('\\')
		qm := zeroByteMask(v ^ quote)
		bm := zeroByteMask(v ^ back)
		if qm != 0 {
			qpos := bits.TrailingZeros64(qm) >> 3
			if bm == 0 || bits.TrailingZeros64(bm)>>3 > qpos {
				return body + qpos + 1
			}
			return -1
		}
		if bm != 0 {
			return -1
		}
		if body+16 <= n {
			v2 := load64(data, body+8)
			qm2 := zeroByteMask(v2 ^ quote)
			bm2 := zeroByteMask(v2 ^ back)
			if qm2 != 0 {
				qpos := bits.TrailingZeros64(qm2) >> 3
				if bm2 == 0 || bits.TrailingZeros64(bm2)>>3 > qpos {
					return body + 8 + qpos + 1
				}
			}
		}
	}
	return -1
}
