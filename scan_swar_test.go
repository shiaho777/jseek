package jseek

import (
	"math/rand"
	"strings"
	"testing"
)

// TestZeroByteMaskProperties verifies the classic SWAR zero-byte detector's
// contract: it never misses a true zero, and any false candidate sits
// directly above a true zero (the borrow-chain artifact). Also verifies the
// saturation-based exact zero detector used by aboveZeroMask.
func TestZeroByteMaskProperties(t *testing.T) {
	check := func(v uint64) {
		var want uint64
		for k := 0; k < 8; k++ {
			if byte(v>>(8*k)) == 0 {
				want |= 0x80 << (8 * k)
			}
		}
		got := zeroByteMask(v)
		if got&want != want {
			t.Fatalf("zeroByteMask(%016x) = %016x misses true zeros %016x", v, got, want)
		}
		if extra := got &^ want; extra &^ (want<<8) != 0 {
			t.Fatalf("zeroByteMask(%016x) = %016x has false candidates %016x not above a zero", v, got, extra)
		}
		// Exact detector must match the scalar reference bit-for-bit.
		if exact := zeroByteMaskExact(v); exact != want {
			t.Fatalf("zeroByteMaskExact(%016x) = %016x, want %016x", v, exact, want)
		}
	}
	for b := 0; b < 256; b++ {
		for pos := 0; pos < 8; pos++ {
			check(uint64(b) << (8 * pos))
		}
	}
	for a := 0; a < 256; a++ {
		for b := 0; b < 256; b++ {
			check(uint64(a) | uint64(b)<<8)
			check(uint64(a)<<8 | uint64(b))
		}
	}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 200000; i++ {
		check(rng.Uint64())
	}
}

// TestNonWhitespaceMaskExact checks the guarded whitespace mask against a
// scalar reference on random words and whitespace-heavy patterns.
func TestNonWhitespaceMaskExact(t *testing.T) {
	check := func(v uint64) {
		var want uint64
		for k := 0; k < 8; k++ {
			c := byte(v >> (8 * k))
			if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
				want |= 0x80 << (8 * k)
			}
		}
		if got := nonWhitespaceMask(v); got != want {
			t.Fatalf("nonWhitespaceMask(%016x) = %016x, want %016x", v, got, want)
		}
	}
	rng := rand.New(rand.NewSource(2))
	for i := 0; i < 200000; i++ {
		check(rng.Uint64())
	}
	// Whitespace runs with punctuation neighbors (the borrow-bug shape).
	for _, s := range []string{
		"        ", "       !", "!       ", " ! ! ! !", "\t\t\t\t\t\t\t\t",
		"\n\r \t\n\r \t", "a      !", "0      !",
	} {
		var v uint64
		for k := 0; k < 8; k++ {
			v |= uint64(s[k]) << (8 * k)
		}
		check(v)
	}
}

// TestIndexQuoteOrBackslash checks the SWAR scanner against a naive byte loop
// across many lengths and alignments, since word-at-a-time code is prone to
// off-by-one and tail-handling bugs at 8-byte boundaries.
func TestIndexQuoteOrBackslash(t *testing.T) {
	naive := func(data []byte, i int) int {
		for ; i < len(data); i++ {
			if data[i] == '"' || data[i] == '\\' {
				return i
			}
		}
		return -1
	}

	bases := []string{
		"",
		"a",
		"abcdefg",
		"abcdefgh",
		"abcdefghi",
		strings.Repeat("x", 31),
		strings.Repeat("y", 64),
		strings.Repeat("z", 100),
	}
	for _, base := range bases {
		b := []byte(base)
		// No special byte present.
		if got, want := indexQuoteOrBackslash(b, 0), naive(b, 0); got != want {
			t.Fatalf("no-match len %d: got %d want %d", len(b), got, want)
		}
		// Insert a quote and a backslash at every position and offset.
		for pos := 0; pos < len(b); pos++ {
			for _, ch := range []byte{'"', '\\'} {
				cp := append([]byte(nil), b...)
				cp[pos] = ch
				for start := 0; start <= pos; start++ {
					if got, want := indexQuoteOrBackslash(cp, start), naive(cp, start); got != want {
						t.Fatalf("len %d pos %d ch %q start %d: got %d want %d",
							len(cp), pos, ch, start, got, want)
					}
					// The 16-byte-wide prototype must agree too.
					if got, want := indexQuoteOrBackslashSWAR16(cp, start), naive(cp, start); got != want {
						t.Fatalf("SWAR16 len %d pos %d ch %q start %d: got %d want %d",
							len(cp), pos, ch, start, got, want)
					}
				}
			}
		}
	}
}

// TestSkipStringLong exercises the SWAR path inside skipString with long
// strings, escapes near word boundaries, and unicode escapes.
func TestSkipStringLong(t *testing.T) {
	cases := []string{
		`"short"`,
		`"` + strings.Repeat("a", 100) + `"`,
		`"` + strings.Repeat("a", 7) + `\n` + strings.Repeat("b", 7) + `"`,
		`"esc\"quote\"inside"`,
		`"unicode \u00e9\u4e2d end"`,
		`"\\\\\\"`,
		`"mix\tof\nescapes\rand\ttext` + strings.Repeat("q", 40) + `"`,
	}
	for _, c := range cases {
		data := []byte(c)
		end, ok := skipString(data, 0)
		if !ok {
			t.Fatalf("skipString failed on %q", c)
		}
		if end != len(data) {
			t.Fatalf("skipString %q: end=%d want %d", c, end, len(data))
		}
	}
}
