package jseek

import "testing"

func TestEachDocMatchesEach(t *testing.T) {
	paths := [][]string{
		{"person", "name", "fullName"},
		{"person", "github", "followers"},
		{"person", "avatars", "[0]", "type"},
		{"company", "name"},
		{"company", "size"},
		{"tags", "[2]"},
		{"does", "not", "exist"},
	}
	q := CompileStrings(paths...)

	// Ground truth: stateless Each.
	wantVal := map[int]string{}
	wantType := map[int]ValueType{}
	q.Each(sample, func(idx int, value []byte, vt ValueType, err error) {
		if err == nil {
			wantVal[idx] = string(value)
			wantType[idx] = vt
		}
	})

	for _, taped := range []bool{false, true} {
		d := Index(sample)
		if taped {
			d.WithTape()
		}
		gotVal := map[int]string{}
		gotType := map[int]ValueType{}
		q.EachDoc(d, func(idx int, value []byte, vt ValueType, err error) {
			if err == nil {
				gotVal[idx] = string(value)
				gotType[idx] = vt
			}
		})
		if len(gotVal) != len(wantVal) {
			t.Fatalf("taped=%v: count got %d want %d", taped, len(gotVal), len(wantVal))
		}
		for idx, wv := range wantVal {
			if gotVal[idx] != wv || gotType[idx] != wantType[idx] {
				t.Errorf("taped=%v path %d: got (%v,%q) want (%v,%q)",
					taped, idx, gotType[idx], gotVal[idx], wantType[idx], wv)
			}
		}
	}
}

func TestEachKeyDocConvenience(t *testing.T) {
	d := IndexTape(sample)
	hits := 0
	EachKeyDoc(d, func(idx int, value []byte, vt ValueType, err error) {
		hits++
	}, []string{"person", "name", "first"}, []string{"person", "name", "last"})
	if hits != 2 {
		t.Fatalf("expected 2 hits, got %d", hits)
	}
}
