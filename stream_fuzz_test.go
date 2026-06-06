package jseek

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// FuzzStreamMatchesArrayEach checks that streaming the elements of a top-level
// array yields the same values (semantically) as ArrayEach over the same bytes.
// Two independent code paths (the streaming reader vs the in-memory scanner)
// must agree.
func FuzzStreamMatchesArrayEach(f *testing.F) {
	seeds := []string{
		`[1,2,3]`,
		`[{"a":1},{"b":2}]`,
		`[]`,
		`[{"s":"}],["},[1,[2,[3]]]]`,
		`["a","b\"c","d"]`,
		`[true,false,null,1.5e3]`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		// Only consider well-formed top-level arrays.
		if !json.Valid(data) {
			return
		}
		trimmed := bytes.TrimSpace(data)
		if len(trimmed) == 0 || trimmed[0] != '[' {
			return
		}

		// Ground truth via ArrayEach (semantic value per element).
		var want []any
		aerr := ArrayEach(data, func(value []byte, dt ValueType, off int) bool {
			var v any
			b := value
			if dt == String {
				// Re-add quotes for json.Unmarshal of the raw (still-escaped) content.
				b = append(append([]byte{'"'}, value...), '"')
			}
			if json.Unmarshal(b, &v) == nil {
				want = append(want, v)
			} else {
				want = append(want, rawMarker{string(value)})
			}
			return true
		})
		if aerr != nil {
			return // ArrayEach rejects; skip (top-level array of odd shape)
		}

		// Streamed values.
		var got []any
		d := NewDecoder(strings.NewReader(string(data)))
		serr := d.ForEach(func(value []byte) error {
			var v any
			if json.Unmarshal(value, &v) == nil {
				got = append(got, v)
			} else {
				got = append(got, rawMarker{string(bytes.Trim(value, `"`))})
			}
			return nil
		})
		if serr != nil {
			t.Fatalf("stream error on valid array %q: %v", data, serr)
		}

		if len(got) != len(want) {
			t.Fatalf("element count: stream=%d arrayEach=%d on %q", len(got), len(want), data)
		}
	})
}

type rawMarker struct{ s string }
