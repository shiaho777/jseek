package jseek

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestCountBasic(t *testing.T) {
	data := []byte(`{"users":[{"a":1},{"a":2},{"a":3}],"empty":[],"obj":{"x":1,"y":2},"eobj":{},"n":5}`)
	cases := []struct {
		keys []string
		want int
	}{
		{[]string{"users"}, 3},
		{[]string{"empty"}, 0},
		{[]string{"obj"}, 2},
		{[]string{"eobj"}, 0},
		{nil, 5}, // top-level object has 5 members
	}
	for _, c := range cases {
		got, err := Count(data, c.keys...)
		if err != nil || got != c.want {
			t.Errorf("Count(%v) = %d, %v; want %d, nil", c.keys, got, err, c.want)
		}
	}
	if _, err := Count(data, "n"); !errors.Is(err, ErrUnexpectedType) {
		t.Errorf("Count on scalar: err = %v, want ErrUnexpectedType", err)
	}
	if _, err := Count(data, "missing"); !errors.Is(err, ErrKeyPathNotFound) {
		t.Errorf("Count on missing path: err = %v, want ErrKeyPathNotFound", err)
	}
	if _, err := Count([]byte(`[1,2`), ); !errors.Is(err, ErrMalformedJSON) {
		t.Errorf("Count on truncated array: err = %v, want ErrMalformedJSON", err)
	}
}

func TestCountIndexedMatchesStateless(t *testing.T) {
	data := []byte(`{"users":[{"a":1},{"a":2},{"a":3}],"obj":{"x":1,"y":2}}`)
	doc := Index(data)
	for _, keys := range [][]string{{"users"}, {"obj"}, nil} {
		want, werr := Count(data, keys...)
		got, gerr := doc.Count(keys...)
		if got != want || (gerr == nil) != (werr == nil) {
			t.Errorf("doc.Count(%v) = %d,%v; stateless = %d,%v", keys, got, gerr, want, werr)
		}
	}
}

func TestCountPathSyntax(t *testing.T) {
	data := []byte(`{"users":[{"a":1},{"a":2}],"obj":{"x":1,"y":2,"z":3}}`)
	cases := []struct {
		path string
		want int64
	}{
		{"users.#", 2},
		{"obj.#", 3},
		{"#", 2}, // top-level object: 2 members
	}
	for _, c := range cases {
		v, vt, _, err := GetPath(data, c.path)
		if err != nil || vt != Number {
			t.Fatalf("GetPath(%q) = %v, %v; want Number", c.path, vt, err)
		}
		n, err := parseInt(v)
		if err != nil || n != c.want {
			t.Errorf("GetPath(%q) = %d (%v), want %d", c.path, n, err, c.want)
		}
		// Indexed engine must agree.
		doc := Index(data)
		dv, dvt, _, derr := doc.GetPath(c.path)
		if derr != nil || dvt != Number || string(dv) != string(v) {
			t.Errorf("doc.GetPath(%q) = %s,%v,%v; want %s", c.path, dv, dvt, derr, v)
		}
	}
	// Internal sentinel segment form works too.
	if n, err := GetInt(data, "users", countSeg); err != nil || n != 2 {
		t.Errorf(`Get(data,"users",countSeg) = %d,%v; want 2`, n, err)
	}
	// "#" in the middle of a path stays an ordinary key.
	d2 := []byte(`{"#":{"x":1}}`)
	if v, _, _, err := GetPath(d2, "#.x"); err != nil || string(v) != "1" {
		t.Errorf(`GetPath("#.x") = %s,%v; want 1`, v, err)
	}
	// A literal "#" object key remains addressable via the native form.
	d3 := []byte(`{"#":7}`)
	if n, err := GetInt(d3, "#"); err != nil || n != 7 {
		t.Errorf(`Get(d3,"#") = %d,%v; want 7`, n, err)
	}
	if !Exists(d3, "#") {
		t.Error(`Exists(d3,"#") = false, want true`)
	}
}

func TestCountZeroAlloc(t *testing.T) {
	data := []byte(`{"users":[{"a":1},{"a":2},{"a":3}]}`)
	allocs := testing.AllocsPerRun(1000, func() {
		if n, err := Count(data, "users"); err != nil || n != 3 {
			panic("bad fixture")
		}
	})
	if allocs != 0 {
		t.Fatalf("Count allocated %v times per run, want 0", allocs)
	}
}

// FuzzCountMatchesStdlib checks Count against a full stdlib decode on valid
// JSON, for the top-level container and one nested path.
func FuzzCountMatchesStdlib(f *testing.F) {
	f.Add([]byte(`{"a":[1,2,3],"b":{"x":1}}`), "a")
	f.Add([]byte(`[{"k":1},{"k":2}]`), "")
	f.Add([]byte(`{}`), "")
	f.Add([]byte(`[]`), "missing")
	f.Fuzz(func(t *testing.T, data []byte, key string) {
		if !json.Valid(data) {
			// Out of contract: only require no panic.
			Count(data)
			Count(data, key)
			return
		}
		// Duplicate keys: Count reports raw member cardinality while a
		// stdlib map deduplicates (jseek documents first-wins extraction).
		// Skip such inputs, as the extraction fuzzers do.
		var top map[string]json.RawMessage
		if json.Unmarshal(data, &top) == nil && hasDuplicateTopLevelKeys(data, len(top)) {
			return
		}
		check := func(keys []string) {
			got, err := Count(data, keys...)
			// Resolve the same target with stdlib.
			var cur any = nil
			if err := json.Unmarshal(data, &cur); err != nil {
				return
			}
			for _, k := range keys {
				if k == "" {
					continue
				}
				m, ok := cur.(map[string]any)
				if !ok {
					cur = nil
					break
				}
				cur, ok = m[k]
				if !ok {
					cur = nil
					break
				}
			}
			var want int
			switch v := cur.(type) {
			case []any:
				want = len(v)
			case map[string]any:
				want = len(v)
			default:
				if err == nil {
					t.Fatalf("Count(%q, %v) = %d,nil but target is %T", data, keys, got, cur)
				}
				return
			}
			if err != nil || got != want {
				t.Fatalf("Count(%q, %v) = %d,%v; want %d,nil", data, keys, got, err, want)
			}
		}
		check(nil)
		if key != "" {
			check([]string{key})
		}
	})
}