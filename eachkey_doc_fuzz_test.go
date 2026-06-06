package jseek

import (
	"encoding/json"
	"testing"
)

// FuzzEachDocMatchesEachKey asserts the index/tape-aware multi-path matcher
// (EachDoc) returns exactly what the stateless byte-scanning matcher (Each)
// returns, for arbitrary valid JSON. Both the untaped and taped Document paths
// are checked.
func FuzzEachDocMatchesEachKey(f *testing.F) {
	seeds := []string{
		`{"a":1,"b":{"c":2,"d":[10,20,{"e":3}]},"f":"hi"}`,
		`{"users":[{"n":"x","t":[1,2]},{"n":"y"}],"k":true}`,
		`[1,2,3,{"a":[4,5]}]`,
		`{"deep":{"deep":{"deep":{"v":42}}}}`,
		`{"a":{},"b":[],"c":{"d":{}}}`,
		`{"dup":1,"dup":2}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	paths := [][]string{
		{"a"}, {"b", "c"}, {"b", "d", "[0]"}, {"b", "d", "[2]", "e"},
		{"f"}, {"users", "[0]", "n"}, {"users", "[0]", "t", "[1]"},
		{"users", "[1]", "n"}, {"k"}, {"deep", "deep", "deep", "v"},
		{"[0]"}, {"[3]", "a", "[1]"}, {"c", "d"}, {"dup"}, {"missing"},
	}
	q := CompileStrings(paths...)

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on %q: %v", data, r)
			}
		}()
		if !json.Valid(data) {
			d := IndexTape(data)
			q.EachDoc(d, func(idx int, value []byte, vt ValueType, err error) {})
			return
		}

		// Ground truth from stateless Each.
		type res struct {
			val string
			vt  ValueType
		}
		want := map[int]res{}
		q.Each(data, func(idx int, value []byte, vt ValueType, err error) {
			if err == nil {
				want[idx] = res{string(value), vt}
			}
		})

		check := func(taped bool) {
			d := Index(data)
			if taped {
				d.WithTape()
			}
			got := map[int]res{}
			q.EachDoc(d, func(idx int, value []byte, vt ValueType, err error) {
				if err == nil {
					got[idx] = res{string(value), vt}
				}
			})
			if len(got) != len(want) {
				t.Fatalf("taped=%v on %q: count got %d want %d", taped, data, len(got), len(want))
			}
			for idx, w := range want {
				g, ok := got[idx]
				if !ok || g != w {
					t.Fatalf("taped=%v path %v on %q: got %+v want %+v", taped, paths[idx], data, g, w)
				}
			}
		}
		check(false)
		check(true)
	})
}
