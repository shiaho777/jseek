package jseek

import (
	"encoding/binary"
	"math/bits"
)

// This file implements SWAR (SIMD Within A Register) scanning: we process eight
// bytes at a time inside a single 64-bit word using bit tricks, instead of one
// byte per loop iteration. This delivers genuine register-level data
// parallelism that works on every architecture Go supports, with no assembly.
//
// These are the portable implementations. The exported entry points
// indexQuoteOrBackslash / indexSkipWhitespace (see scan_dispatch_*.go) dispatch
// to either these or to hardware-SIMD kernels, so a SIMD build can replace the
// hot loops without touching any caller.

const (
	swarLSB = 0x0101010101010101
	swarMSB = 0x8080808080808080
)

// broadcast returns a word with c repeated in all eight byte lanes.
func broadcast(c byte) uint64 {
	return uint64(c) * swarLSB
}

// zeroByteMask returns a word whose high bit is set in each lane that was zero
// in v. This is the classic "find a zero byte" trick: (v-1) borrows into the
// high bit only where the byte was zero, masked against ^v to suppress false
// positives from bytes that already had the high bit set.
func zeroByteMask(v uint64) uint64 {
	return (v - swarLSB) & ^v & swarMSB
}

// indexQuoteOrBackslashSWAR scans data from i for the first '"' or '\',
// processing eight bytes per step. Returns the index, or -1 if neither appears.
func indexQuoteOrBackslashSWAR(data []byte, i int) int {
	n := len(data)
	quote := broadcast('"')
	back := broadcast('\\')

	for i+8 <= n {
		v := binary.LittleEndian.Uint64(data[i : i+8])
		m := zeroByteMask(v^quote) | zeroByteMask(v^back)
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

// indexQuoteOrBackslashSWAR16 is a prototype that processes sixteen bytes per
// step using two 64-bit words. It models the benefit of a wider vector lane
// (the essence of what AVX2/NEON would buy) using only portable Go, so the
// payoff of wider scanning can be measured on real hardware before committing
// to architecture-specific assembly.
func indexQuoteOrBackslashSWAR16(data []byte, i int) int {
	n := len(data)
	quote := broadcast('"')
	back := broadcast('\\')

	for i+16 <= n {
		v0 := binary.LittleEndian.Uint64(data[i : i+8])
		v1 := binary.LittleEndian.Uint64(data[i+8 : i+16])
		m0 := zeroByteMask(v0^quote) | zeroByteMask(v0^back)
		if m0 != 0 {
			return i + bits.TrailingZeros64(m0)>>3
		}
		m1 := zeroByteMask(v1^quote) | zeroByteMask(v1^back)
		if m1 != 0 {
			return i + 8 + bits.TrailingZeros64(m1)>>3
		}
		i += 16
	}
	for i+8 <= n {
		v := binary.LittleEndian.Uint64(data[i : i+8])
		m := zeroByteMask(v^quote) | zeroByteMask(v^back)
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

// indexSkipWhitespaceSWAR advances past a run of JSON whitespace eight bytes at
// a time, returning the index of the first non-whitespace byte.
func indexSkipWhitespaceSWAR(data []byte, i int) int {
	n := len(data)
	for i+8 <= n {
		v := binary.LittleEndian.Uint64(data[i : i+8])
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

// nonWhitespaceMask returns a word whose high bit is set in each lane that is
// NOT JSON whitespace.
func nonWhitespaceMask(v uint64) uint64 {
	isSpace := zeroByteMask(v ^ broadcast(' '))
	isTab := zeroByteMask(v ^ broadcast('\t'))
	isNL := zeroByteMask(v ^ broadcast('\n'))
	isCR := zeroByteMask(v ^ broadcast('\r'))
	ws := isSpace | isTab | isNL | isCR
	return ^ws & swarMSB
}
