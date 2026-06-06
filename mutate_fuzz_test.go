package jseek

import (
	"encoding/json"
	"reflect"
	"testing"
	"unicode/utf8"
)

// normalize round-trips data through encoding/json to a canonical Go value, or
// reports that it is not valid JSON.
func normalize(b []byte) (any, bool) {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, false
	}
	return v, true
}

// FuzzSetAgainstStdlib verifies that Set on a top-level object key produces
// valid JSON whose decoded form equals the stdlib map with that key updated.
func FuzzSetAgainstStdlib(f *testing.F) {
	seeds := []struct {
		data, key, val string
	}{
		{`{"a":1,"b":2}`, "a", "42"},
		{`{"a":1}`, "c", `"new"`},
		{`{}`, "x", "true"},
		{`{"a":{"b":1}}`, "a", "null"},
		{`{"nested":{"x":1},"k":2}`, "k", `[1,2,3]`},
	}
	for _, s := range seeds {
		f.Add([]byte(s.data), s.key, []byte(s.val))
	}

	f.Fuzz(func(t *testing.T, data []byte, key string, val []byte) {
		// Stay within jseek's contract: valid UTF-8, valid base object, valid
		// replacement value, and a deduplicated object (Set targets the first
		// occurrence; stdlib remap collapses duplicates).
		if !utf8.Valid(data) || !utf8.ValidString(key) || !utf8.Valid(val) {
			return
		}
		var base map[string]json.RawMessage
		if err := json.Unmarshal(data, &base); err != nil {
			return
		}
		// normalize (full stdlib parse) validates the output, so hold the input
		// to the same bar: RawMessage is lazy and accepts float64-overflowing
		// numbers like 1e1000 that the full parse rejects.
		if _, ok := normalize(data); !ok {
			return
		}
		// Require an actual object (json unmarshals null/other into a nil map).
		if ts := skipWhitespace(data, 0); ts >= len(data) || data[ts] != '{' {
			return
		}
		var setv any
		if err := json.Unmarshal(val, &setv); err != nil {
			return // setValue must be valid JSON
		}
		if hasDuplicateTopLevelKeys(data, len(base)) {
			return
		}

		out, err := Set(data, val, key)
		if err != nil {
			t.Fatalf("Set error on %q key %q val %q: %v", data, key, val, err)
		}
		got, ok := normalize(out)
		if !ok {
			t.Fatalf("Set produced invalid JSON: %s (from %q, key %q, val %q)", out, data, key, val)
		}

		// Build the expectation with the standard library.
		want := map[string]any{}
		for k, raw := range base {
			var v any
			_ = json.Unmarshal(raw, &v)
			want[k] = v
		}
		want[key] = setv

		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Set mismatch: got %#v want %#v (data %q key %q val %q)", got, want, data, key, val)
		}
	})
}

// FuzzDeleteAgainstStdlib verifies that Delete on a top-level object key
// produces valid JSON equal to the stdlib map with that key removed.
func FuzzDeleteAgainstStdlib(f *testing.F) {
	seeds := []struct{ data, key string }{
		{`{"a":1,"b":2}`, "a"},
		{`{"a":1,"b":2,"c":3}`, "b"},
		{`{"a":1}`, "a"},
		{`{"a":1}`, "missing"},
		{`{"x":{"y":1}}`, "x"},
	}
	for _, s := range seeds {
		f.Add([]byte(s.data), s.key)
	}

	f.Fuzz(func(t *testing.T, data []byte, key string) {
		if !utf8.Valid(data) || !utf8.ValidString(key) {
			return
		}
		var base map[string]json.RawMessage
		if err := json.Unmarshal(data, &base); err != nil {
			return
		}
		// The output is validated by normalize (a full stdlib parse), so the
		// input must clear that same bar. RawMessage above is lazy and accepts
		// values like 1e1000 that overflow float64 on a full parse; restrict to
		// inputs that fully round-trip so the oracle and the guard agree.
		if _, ok := normalize(data); !ok {
			return
		}
		if ts := skipWhitespace(data, 0); ts >= len(data) || data[ts] != '{' {
			return
		}
		if hasDuplicateTopLevelKeys(data, len(base)) {
			return
		}

		out := Delete(data, key)
		got, ok := normalize(out)
		if !ok {
			t.Fatalf("Delete produced invalid JSON: %s (from %q key %q)", out, data, key)
		}

		want := map[string]any{}
		for k, raw := range base {
			if k == key {
				continue
			}
			var v any
			_ = json.Unmarshal(raw, &v)
			want[k] = v
		}

		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Delete mismatch: got %#v want %#v (data %q key %q)", got, want, data, key)
		}
	})
}
