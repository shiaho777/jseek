package jseek

import (
	"encoding/json"
	"testing"
)

// FuzzDocumentMatchesGet asserts the indexed Stage-1/Stage-2 engine returns
// exactly what the stateless re-scanning Get returns, for arbitrary inputs and
// a fixed battery of paths. The two share valueAt for extraction, so this
// isolates the structural-index navigation logic.
func FuzzDocumentMatchesGet(f *testing.F) {
	seeds := []string{
		`{"a":1,"b":{"c":2,"d":[10,20,{"e":3}]},"f":"hi"}`,
		`{"users":[{"n":"x","t":[1,2]},{"n":"y"}],"k":true}`,
		`[1,2,3,{"a":[4,5]}]`,
		`{"deep":{"deep":{"deep":{"v":42}}}}`,
		`{"s":"a\nb\t\"c\"","u":"\u00e9\ud83d\ude00"}`,
		`{}`,
		`[]`,
		`null`,
		`{"a":{},"b":[],"c":{"d":{}}}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	paths := [][]string{
		{},
		{"a"},
		{"b", "c"},
		{"b", "d", "[0]"},
		{"b", "d", "[2]", "e"},
		{"f"},
		{"users", "[0]", "n"},
		{"users", "[0]", "t", "[1]"},
		{"users", "[1]", "n"},
		{"k"},
		{"deep", "deep", "deep", "v"},
		{"[0]"},
		{"[3]", "a", "[1]"},
		{"s"},
		{"u"},
		{"c", "d"},
		{"missing"},
		{"a", "b", "c"},
		{"users", "[9]", "n"},
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on %q: %v", data, r)
			}
		}()

		// The indexed and stateless engines are guaranteed to agree on valid
		// JSON. On malformed input their error-recovery paths legitimately
		// differ, which is outside the contract, so restrict to valid JSON.
		if !json.Valid(data) {
			d := Index(data)
			for _, p := range paths {
				_, _, _, _ = d.Get(p...) // must not panic
			}
			return
		}

		d := Index(data)
		for _, p := range paths {
			wv, wt, wo, werr := Get(data, p...)
			gv, gt, go_, gerr := d.Get(p...)

			if (werr == nil) != (gerr == nil) {
				t.Fatalf("path %v on %q: err presence differs stateless=%v indexed=%v", p, data, werr, gerr)
			}
			if werr != nil {
				continue
			}
			if wt != gt {
				t.Fatalf("path %v on %q: type stateless=%v indexed=%v", p, data, wt, gt)
			}
			if string(wv) != string(gv) {
				t.Fatalf("path %v on %q: value stateless=%q indexed=%q", p, data, wv, gv)
			}
			if wo != go_ {
				t.Fatalf("path %v on %q: offset stateless=%d indexed=%d", p, data, wo, go_)
			}
		}
	})
}
