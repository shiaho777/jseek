package jseek

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// TestScanKeyWideMatchesScalar cross-checks the SWAR wide key scanner against
// the scalar reference on long keys, mismatch positions, escapes, and
// truncation — the wide path must never change match or extent semantics.
func TestScanKeyWideMatchesScalar(t *testing.T) {
	targets := []string{
		strings.Repeat("a", 8),
		strings.Repeat("abcdefgh", 5), // 40 bytes
		"organization_login_name",
		strings.Repeat("x", 7) + "\\", // unsafe tail -> scalar dispatch
		strings.Repeat("x", 9) + "\"",
		strings.Repeat("x", 9) + "\x01",
	}
	for _, key := range targets {
		for klen := 0; klen <= len(key)+2; klen++ {
			docKey := key
			if klen <= len(key) {
				docKey = key[:klen]
			} else {
				docKey = key + strings.Repeat("z", klen-len(key))
			}
			for _, esc := range []string{"", `\n`, `A`, `\\`} {
				raw := `{"` + docKey + esc + `":1,"other":2}`
				data := []byte(raw)
				// Locate the opening quote of the first key.
				i := 1
				wEnd, wMatch, wOK := scanKey(data, i, key)
				sEnd, sMatch, sOK := scanKeyScalar(data, i, key)
				if wEnd != sEnd || wMatch != sMatch || wOK != sOK {
					t.Fatalf("scanKey divergence key=%q doc=%q esc=%q: wide=(%d,%v,%v) scalar=(%d,%v,%v)",
						key, docKey, esc, wEnd, wMatch, wOK, sEnd, sMatch, sOK)
				}
				// End-to-end extraction must find the value iff matched.
				v, _, _, err := Get(data, key)
				if wMatch && (err != nil || string(v) != "1") {
					t.Fatalf("Get(%q) on %q = %s,%v; want 1", key, raw, v, err)
				}
				if !wMatch && err == nil {
					t.Fatalf("Get(%q) on %q unexpectedly found %s", key, raw, v)
				}
			}
		}
	}
	// Truncated documents: no panic; an unterminated key body fails the
	// scan, while a closed key is a complete scan even if the value is cut.
	for _, raw := range []string{`{"abcdefghij`, `{"abcdefghijklmnop`} {
		data := []byte(raw)
		_, _, ok := scanKey(data, 1, "abcdefghijklmnopqrst")
		if ok {
			t.Fatalf("scanKey on truncated %q reported ok", raw)
		}
	}
	if _, _, ok := scanKey([]byte(`{"abcdefghij"`), 1, "abcdefghijklmnopqrst"); !ok {
		t.Fatal("scanKey on closed key reported failure")
	}
}

func TestParseIntBoundaries(t *testing.T) {
	cases := []struct {
		raw  string
		want int64
		err  error
	}{
		{"0", 0, nil},
		{"-0", 0, nil},
		{fmt.Sprintf("%d", int64(math.MaxInt64)), math.MaxInt64, nil},
		{fmt.Sprintf("%d", int64(math.MinInt64)), math.MinInt64, nil},
		{fmt.Sprintf("%d", uint64(math.MaxInt64)+1), 0, ErrOverflow},  // MaxInt64+1
		{fmt.Sprintf("-%d", uint64(math.MaxInt64)+2), 0, ErrOverflow}, // MinInt64-1
		{fmt.Sprintf("%d", uint64(math.MaxUint64)), 0, ErrOverflow},   // MaxUint64
		{"1" + strings.Repeat("0", 20), 0, ErrOverflow}, // 10^20 > MaxUint64
		{"99911111111111999999", 0, ErrOverflow},
		{"1001111111111100000000000000", 0, ErrOverflow},
		{"12.5", 0, ErrUnexpectedType},
		{"1e3", 0, ErrUnexpectedType},
		{"", 0, ErrMalformedJSON},
		{"-", 0, ErrMalformedJSON},
	}
	for _, c := range cases {
		got, err := parseInt([]byte(c.raw))
		if got != c.want || err != c.err {
			t.Errorf("parseInt(%q) = %d,%v; want %d,%v", c.raw, got, err, c.want, c.err)
		}
	}
}

func TestPathQueriesZeroAlloc(t *testing.T) {
	data := []byte(`{"user":{"name":"Ada","followers":42},"tags":["a","b"]}`)
	allocs := testing.AllocsPerRun(1000, func() {
		if v, _, _, err := GetPath(data, "user.name"); err != nil || string(v) != "Ada" {
			panic("GetPath failed")
		}
		if v, _, _, err := GetPath(data, "tags[1]"); err != nil || string(v) != "b" {
			panic("GetPath bracket failed")
		}
		if v, _, _, err := GetPointer(data, "/user/followers"); err != nil || string(v) != "42" {
			panic("GetPointer failed")
		}
	})
	if allocs != 0 {
		t.Fatalf("simple path queries allocated %v times per run, want 0", allocs)
	}
}

// TestPathFastMatchesParser checks the zero-allocation parsers against the
// allocating reference parsers on a broad set of paths.
func TestPathFastMatchesParser(t *testing.T) {
	paths := []string{
		"", "a", "a.b", "a.b.c", "users[0].name", "a[0][1]", "a..b",
		"a.", ".a", "a[0]x", "a[b]", "x.y[12].z", "a\\.b", "a.0", "0",
		"a[0", "#", "users.#", "a.b.#", "a.#.b",
	}
	var buf [maxStackSegs]string
	for _, p := range paths {
		want := ParseDotPath(p)
		got, ok := parseDotPathInto(buf[:0], p)
		if !ok {
			continue // documented fallback subset
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("parseDotPathInto(%q) = %v; ParseDotPath = %v", p, got, want)
		}
	}
	pointers := []string{
		"", "/a", "/a/b", "/a/0", "/a~1b", "/a~0b", "/", "/a/", "//",
		"/a/b/c/d/e/f/g/h/i/j/k/l/m/n/o/p/q", // >16 tokens: fallback
	}
	for _, p := range pointers {
		want, werr := ParsePointer(p)
		got, ok, gerr := parsePointerInto(buf[:0], p)
		if (werr == nil) != (gerr == nil) {
			t.Fatalf("parsePointerInto(%q) err=%v; ParsePointer err=%v", p, gerr, werr)
		}
		if !ok {
			continue
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("parsePointerInto(%q) = %v; ParsePointer = %v", p, got, want)
		}
	}
}

func BenchmarkGetStringLong(b *testing.B) {
	data := []byte(`{"ua":"Mozilla/5.0 (compatible; MSIE 10.0; Windows NT 6.2; WOW64; Trident/6.0)","n":1}`)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := GetString(data, "ua"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkKeyMatchesLong(b *testing.B) {
	key := "organization_repository_full_name"
	data := []byte(`{"` + key + `":"value"}`)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if !keyMatches(data, 2, 2+len(key), key) {
			b.Fatal("must match")
		}
	}
}

func BenchmarkKeyMatchesVeryLong(b *testing.B) {
	key := strings.Repeat("organization_repository_", 4) + "full_name" // 93 bytes
	data := []byte(`{"` + key + `":"value"}`)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if !keyMatches(data, 2, 2+len(key), key) {
			b.Fatal("must match")
		}
	}
}

func BenchmarkScanKeyShort(b *testing.B) {
	data := []byte(`{"name":"Ada","followers":42,"admin":true}`)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		scanKey(data, 1, "name")
	}
}

func BenchmarkScanKeyLong(b *testing.B) {
	key := "organization_repository_full_name"
	data := []byte(`{"` + key + `":"value","other":1}`)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		scanKey(data, 1, key)
	}
}

func BenchmarkGetIntLarge(b *testing.B) {
	data := []byte(`{"n":9221111111111175807}`)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := GetInt(data, "n"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkValidLarge(b *testing.B) {
	data := []byte(`{"users":[` + strings.Repeat(`{"name":"Ada","followers":42,"admin":false},`, 499) + `{"name":"Bob","followers":7,"admin":true}]}`)
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		if !Valid(data) {
			b.Fatal("fixture must be valid")
		}
	}
}

func BenchmarkCountArray(b *testing.B) {
	data := []byte(`{"users":[` + strings.Repeat(`{"name":"Ada","followers":42},`, 499) + `{"name":"Bob","followers":7}]}`)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if n, err := Count(data, "users"); err != nil || n != 500 {
			b.Fatal("bad count")
		}
	}
}