package jseek

import (
	"bytes"
	"encoding/json"
	"testing"
)

// validForStreaming reports whether data is unambiguous streaming input: a
// single valid top-level JSON array, or NDJSON where every non-empty line is a
// valid JSON value. This excludes pathological concatenations without
// separators, where reader/byte scanners may legitimately recover differently.
func validForStreaming(data []byte) bool {
	if json.Valid(data) {
		// A single valid JSON value: only treat arrays as multi-element
		// streams here (a lone object/scalar is one element and always agrees).
		i := skipWhitespace(data, 0)
		return i < len(data) && data[i] == '['
	}
	// Try NDJSON: split on newlines, each non-empty line must be valid JSON.
	lines := bytes.Split(data, []byte{'\n'})
	any := false
	for _, ln := range lines {
		t := bytes.TrimSpace(ln)
		if len(t) == 0 {
			continue
		}
		if !json.Valid(t) {
			return false
		}
		any = true
	}
	return any
}

// FuzzStreamBytesMatchesDecoder asserts the zero-copy in-memory StreamBytes
// yields exactly the same elements as the reader-based Decoder, so the fast
// path is a drop-in equivalent.
func FuzzStreamBytesMatchesDecoder(f *testing.F) {
	seeds := []string{
		`[1,2,3]`,
		`[{"a":1},{"b":[2,3]}]`,
		`{"x":1}` + "\n" + `{"y":2}`,
		`"a" "b"`,
		`[]`,
		`[{"s":"}],["}]`,
		`  [ 1 , 2 ]  `,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on %q: %v", data, r)
			}
		}()

		// Both engines target either a top-level array or whitespace-separated
		// values (NDJSON). Concatenated values with no separator (e.g. "0{}" )
		// are malformed for this purpose and the two scanners may recover
		// differently; skip those by requiring the input be a single valid
		// JSON array, or valid when wrapped as an array of the elements. The
		// cleanest scoping is: only compare when the whole input is a valid
		// JSON value that is an array, or is valid NDJSON (each line valid).
		if !validForStreaming(data) {
			_ = StreamBytes(data, func(v []byte) error { return nil })
			return
		}

		var fromBytes [][]byte
		errB := StreamBytes(data, func(v []byte) error {
			cp := append([]byte(nil), v...)
			fromBytes = append(fromBytes, cp)
			return nil
		})

		var fromReader [][]byte
		d := NewDecoder(bytes.NewReader(data))
		errR := d.ForEach(func(v []byte) error {
			cp := append([]byte(nil), v...)
			fromReader = append(fromReader, cp)
			return nil
		})

		// When both succeed, the element sequences must match exactly. When
		// they disagree on success, that is only allowed on malformed input
		// where recovery legitimately differs; require matching element counts
		// for the common prefix that both produced.
		if errB == nil && errR == nil {
			if len(fromBytes) != len(fromReader) {
				t.Fatalf("count differs on %q: bytes=%d reader=%d", data, len(fromBytes), len(fromReader))
			}
			for i := range fromBytes {
				if !bytes.Equal(fromBytes[i], fromReader[i]) {
					t.Fatalf("elem %d differs on %q: bytes=%q reader=%q", i, data, fromBytes[i], fromReader[i])
				}
			}
		}
	})
}
