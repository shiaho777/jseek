package jseek

import (
	"strings"
	"testing"
)

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
