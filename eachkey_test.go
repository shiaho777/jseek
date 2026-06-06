package jseek

import "testing"

func TestEachKey(t *testing.T) {
	results := map[int]string{}
	types := map[int]ValueType{}
	EachKeyStrings(sample, func(idx int, value []byte, vt ValueType, err error) {
		if err != nil {
			t.Errorf("path %d error: %v", idx, err)
			return
		}
		results[idx] = string(value)
		types[idx] = vt
	},
		[]string{"person", "name", "fullName"},
		[]string{"person", "github", "followers"},
		[]string{"person", "avatars", "[0]", "type"},
		[]string{"company", "name"},
		[]string{"does", "not", "exist"},
	)

	if results[0] != "Leonid Bugaev" || types[0] != String {
		t.Errorf("path 0: got %q (%v)", results[0], types[0])
	}
	if results[1] != "109" || types[1] != Number {
		t.Errorf("path 1: got %q (%v)", results[1], types[1])
	}
	if results[2] != "thumbnail" || types[2] != String {
		t.Errorf("path 2: got %q (%v)", results[2], types[2])
	}
	if results[3] != "Acme" || types[3] != String {
		t.Errorf("path 3: got %q (%v)", results[3], types[3])
	}
	if _, ok := results[4]; ok {
		t.Errorf("path 4 should not have matched, got %q", results[4])
	}
}

func TestEachKeySharedPrefix(t *testing.T) {
	// Two paths sharing the "person"/"name" prefix should both resolve in one
	// pass.
	hits := 0
	EachKeyStrings(sample, func(idx int, value []byte, vt ValueType, err error) {
		hits++
	},
		[]string{"person", "name", "first"},
		[]string{"person", "name", "last"},
	)
	if hits != 2 {
		t.Fatalf("expected 2 hits, got %d", hits)
	}
}

func TestEachKeyValuesMatchGet(t *testing.T) {
	// EachKey must agree with Get for every requested path.
	paths := [][]string{
		{"person", "name", "fullName"},
		{"person", "github", "followers"},
		{"company", "size"},
		{"person", "active"},
		{"person", "score"},
	}
	want := make([]string, len(paths))
	for i, p := range paths {
		v, _, _, err := Get(sample, p...)
		if err != nil {
			t.Fatalf("Get failed for %v: %v", p, err)
		}
		want[i] = string(v)
	}
	EachKeyStrings(sample, func(idx int, value []byte, vt ValueType, err error) {
		if err != nil {
			t.Errorf("path %d: %v", idx, err)
			return
		}
		if string(value) != want[idx] {
			t.Errorf("path %d: EachKey=%q Get=%q", idx, value, want[idx])
		}
	}, paths...)
}
