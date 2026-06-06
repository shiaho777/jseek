package jseek

import (
	"encoding/json"
	"testing"
)

// FuzzTapeMatchesGet asserts the tape-accelerated navigation returns exactly
// what the stateless re-scanning Get returns, for arbitrary valid JSON and a
// battery of paths. This guards the O(1) skip path against the linear one.
func FuzzTapeMatchesGet(f *testing.F) {
	seeds := []string{
		`{"a":1,"b":{"c":2,"d":[10,20,{"e":3}]},"f":"hi"}`,
		`{"users":[{"n":"x","t":[1,2]},{"n":"y"}],"k":true}`,
		`[1,2,3,{"a":[4,5]}]`,
		`{"deep":{"deep":{"deep":{"v":42}}}}`,
		`{"a":{},"b":[],"c":{"d":{}}}`,
		`[[[[1]]]]`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	paths := [][]string{
		{}, {"a"}, {"b", "c"}, {"b", "d", "[0]"}, {"b", "d", "[2]", "e"},
		{"f"}, {"users", "[0]", "n"}, {"users", "[0]", "t", "[1]"},
		{"users", "[1]", "n"}, {"k"}, {"deep", "deep", "deep", "v"},
		{"[0]"}, {"[3]", "a", "[1]"}, {"c", "d"}, {"missing"},
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on %q: %v", data, r)
			}
		}()
		if !json.Valid(data) {
			taped := IndexTape(data)
			for _, p := range paths {
				_, _, _, _ = taped.Get(p...)
			}
			return
		}
		taped := IndexTape(data)
		for _, p := range paths {
			wv, wt, wo, werr := Get(data, p...)
			gv, gt, go_, gerr := taped.Get(p...)
			if (werr == nil) != (gerr == nil) {
				t.Fatalf("path %v on %q: err presence differs stateless=%v tape=%v", p, data, werr, gerr)
			}
			if werr != nil {
				continue
			}
			if wt != gt || string(wv) != string(gv) || wo != go_ {
				t.Fatalf("path %v on %q: stateless=(%v,%q,%d) tape=(%v,%q,%d)", p, data, wt, wv, wo, gt, gv, go_)
			}
		}
	})
}
