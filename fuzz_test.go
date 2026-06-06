package jseek

import (
	"bytes"
	"encoding/json"
	"testing"
	"unicode/utf8"
)

// hasDuplicateTopLevelKeys reports whether the top-level object in data has
// more physical members than mapLen (the size of the deduplicated stdlib map),
// which indicates duplicate keys.
func hasDuplicateTopLevelKeys(data []byte, mapLen int) bool {
	count := 0
	_ = ObjectEach(data, func(key, value []byte, dt ValueType, off int) bool {
		count++
		return true
	})
	return count != mapLen
}

// FuzzGetAgainstStdlib is a differential fuzz test: for a generated JSON
// document and a single top-level key, jseek must agree with encoding/json about
// whether the key exists and, for scalar values, what it decodes to. This is
// how we defend the "fast without sacrificing correctness" claim.
func FuzzGetAgainstStdlib(f *testing.F) {
	seeds := []string{
		`{"a":1,"b":"two","c":true,"d":null,"e":[1,2,3],"f":{"g":1.5}}`,
		`{"name":"Ada","age":36}`,
		`[1,2,3]`,
		`{"escaped":"a\nb\t\"c\"","u":"\u00e9"}`,
		`{}`,
		`{"nested":{"deep":{"value":42}}}`,
		`{"big":123456789012345,"neg":-987,"flt":3.14159e2}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s), "a")
		f.Add([]byte(s), "name")
		f.Add([]byte(s), "missing")
	}

	f.Fuzz(func(t *testing.T, data []byte, key string) {
		// jseek's correctness contract is defined over RFC 8259-compliant JSON,
		// which must be valid UTF-8. encoding/json instead performs lossy
		// U+FFFD replacement on invalid bytes, so the two intentionally diverge
		// on malformed input. Restrict the differential comparison to valid
		// UTF-8 inputs (and keys), where there is a single unambiguous answer.
		if !utf8.Valid(data) || !utf8.ValidString(key) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("jseek panicked on input %q key %q: %v", data, key, r)
				}
			}()
			_, _, _, _ = Get(data, key)
			return
		}

		// Establish ground truth with the standard library.
		var top map[string]json.RawMessage
		if err := json.Unmarshal(data, &top); err != nil {
			// Not a JSON object at the top level; jseek should not panic, and
			// Get should report not-found / malformed without crashing.
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("jseek panicked on non-object input %q: %v", data, r)
				}
			}()
			_, _, _, _ = Get(data, key)
			return
		}

		raw, present := top[key]

		// Duplicate keys are implementation-defined per RFC 8259. encoding/json
		// keeps the last occurrence; jseek returns the first (matching
		// jsonparser and gjson). Skip the comparison when the top-level object
		// contains duplicate keys so the test doesn't penalize this choice.
		if hasDuplicateTopLevelKeys(data, len(top)) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("jseek panicked on duplicate-key input %q: %v", data, r)
				}
			}()
			_, _, _, _ = Get(data, key)
			return
		}

		val, vt, _, err := Get(data, key)

		if !present {
			if err == nil {
				t.Fatalf("jseek found key %q that stdlib says is absent in %q", key, data)
			}
			return
		}
		if err != nil {
			t.Fatalf("jseek missed key %q present per stdlib in %q: %v", key, data, err)
		}

		// Compare scalar decodings where both sides have a clear answer.
		switch vt {
		case String:
			var want string
			if json.Unmarshal(raw, &want) == nil {
				got, gerr := GetString(data, key)
				if gerr != nil || got != want {
					t.Fatalf("string mismatch for %q: jseek=%q(%v) stdlib=%q in %q", key, got, gerr, want, data)
				}
			}
		case Boolean:
			var want bool
			if json.Unmarshal(raw, &want) == nil {
				got, gerr := GetBoolean(data, key)
				if gerr != nil || got != want {
					t.Fatalf("bool mismatch for %q: jseek=%v stdlib=%v", key, got, want)
				}
			}
		case Number:
			// Compare against the trimmed raw token bytes; jseek returns the
			// verbatim number token.
			trimmed := bytes.TrimSpace(raw)
			if !bytes.Equal(bytes.TrimSpace(val), trimmed) {
				t.Fatalf("number token mismatch for %q: jseek=%q stdlib=%q", key, val, trimmed)
			}
		}
	})
}

// FuzzEachKeyMatchesGet ensures the single-pass multi-path matcher always
// agrees with repeated single-path Get calls. Any divergence between the two
// code paths (e.g. a SWAR or trie bug) is caught here.
func FuzzEachKeyMatchesGet(f *testing.F) {
	seeds := []string{
		`{"a":1,"b":{"c":2,"d":[10,20,30]},"e":"hi"}`,
		`{"users":[{"n":"x"},{"n":"y"}]}`,
		`{"a":{"b":{"c":{"d":42}}}}`,
		`[{"k":1},{"k":2},{"k":3}]`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	// A fixed, representative set of query paths.
	paths := [][]string{
		{"a"},
		{"b", "c"},
		{"b", "d", "[1]"},
		{"e"},
		{"users", "[0]", "n"},
		{"users", "[1]", "n"},
		{"a", "b", "c", "d"},
		{"[0]", "k"},
		{"[2]", "k"},
		{"missing", "path"},
	}
	compiled := CompileStrings(paths...)

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on input %q: %v", data, r)
			}
		}()

		// Ground truth: what Get returns for each path.
		type res struct {
			val []byte
			vt  ValueType
			ok  bool
		}
		want := make([]res, len(paths))
		for i, p := range paths {
			v, vt, _, err := Get(data, p...)
			want[i] = res{val: v, vt: vt, ok: err == nil}
		}

		seen := make([]bool, len(paths))
		compiled.Each(data, func(idx int, value []byte, vt ValueType, err error) {
			seen[idx] = true
			w := want[idx]
			if err != nil {
				// EachKey only reports malformed where Get also fails.
				if w.ok {
					t.Fatalf("path %v: EachKey err %v but Get succeeded on %q", paths[idx], err, data)
				}
				return
			}
			if !w.ok {
				t.Fatalf("path %v: EachKey matched but Get failed on %q", paths[idx], data)
			}
			if vt != w.vt {
				t.Fatalf("path %v: type EachKey=%v Get=%v on %q", paths[idx], vt, w.vt, data)
			}
			if string(value) != string(w.val) {
				t.Fatalf("path %v: value EachKey=%q Get=%q on %q", paths[idx], value, w.val, data)
			}
		})

		// Every path Get found must also be reported by EachKey.
		for i := range paths {
			if want[i].ok && !seen[i] {
				t.Fatalf("path %v: Get found it but EachKey did not on %q", paths[i], data)
			}
		}
	})
}
