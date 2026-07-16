//go:build amd64 && !purego

package jseek

import "math/bits"

const stringAVXSoftLimit = 256

func skipStringBody(data []byte, i int) (int, bool) {
	n := len(data)
	quote := broadcast('"')
	back := broadcast('\\')
	limit := i + stringAVXSoftLimit
	if limit > n {
		limit = n
	}
	for {
		for i+32 <= limit {
			v0 := load64(data, i)
			m0 := quoteBackslashMask(v0, quote, back)
			if m0 != 0 {
				j := i + bits.TrailingZeros64(m0)>>3
				if data[j] == '"' {
					return j + 1, true
				}
				i = j + 2
				goto cont
			}
			v1 := load64(data, i+8)
			m1 := quoteBackslashMask(v1, quote, back)
			if m1 != 0 {
				j := i + 8 + bits.TrailingZeros64(m1)>>3
				if data[j] == '"' {
					return j + 1, true
				}
				i = j + 2
				goto cont
			}
			v2 := load64(data, i+16)
			m2 := quoteBackslashMask(v2, quote, back)
			if m2 != 0 {
				j := i + 16 + bits.TrailingZeros64(m2)>>3
				if data[j] == '"' {
					return j + 1, true
				}
				i = j + 2
				goto cont
			}
			v3 := load64(data, i+24)
			m3 := quoteBackslashMask(v3, quote, back)
			if m3 != 0 {
				j := i + 24 + bits.TrailingZeros64(m3)>>3
				if data[j] == '"' {
					return j + 1, true
				}
				i = j + 2
				goto cont
			}
			i += 32
		}
		for i+8 <= limit {
			v := load64(data, i)
			m := quoteBackslashMask(v, quote, back)
			if m != 0 {
				j := i + bits.TrailingZeros64(m)>>3
				if data[j] == '"' {
					return j + 1, true
				}
				i = j + 2
				goto cont
			}
			i += 8
		}
		for i < limit {
			c := data[i]
			if c == '"' {
				return i + 1, true
			}
			if c == '\\' {
				i += 2
				goto cont
			}
			i++
		}
		if i >= n {
			return n, false
		}
		return skipStringBodyAVX2(data, i)
	cont:
		if i >= n {
			return n, false
		}
		if i >= limit {
			if i >= n {
				return n, false
			}
			return skipStringBodyAVX2(data, i)
		}
	}
}
