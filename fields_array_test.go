package jseek

import (
	"strconv"
	"testing"
)

func TestEachArrayFields(t *testing.T) {
	var b []byte
	b = append(b, `{"users":[`...)
	for i := 0; i < 20; i++ {
		if i > 0 {
			b = append(b, ',')
		}
		id := strconv.Itoa(i)
		b = append(b, `{"id":`...)
		b = append(b, id...)
		b = append(b, `,"username":"u`...)
		b = append(b, id...)
		b = append(b, `","followers":`...)
		b = append(b, strconv.Itoa(i*7)...)
		b = append(b, '}')
	}
	b = append(b, `]}`...)

	type hit struct {
		elem, key int
		val       string
	}
	var hits []hit
	err := EachArrayFields(b, []string{"users"}, []string{"username", "followers", "missing"},
		func(elem, key int, value []byte, vt ValueType, e error) bool {
			if key == 2 {
				if e != ErrKeyPathNotFound {
					t.Fatalf("missing key err: %v", e)
				}
				return true
			}
			if e != nil {
				t.Fatalf("elem=%d key=%d: %v", elem, key, e)
			}
			hits = append(hits, hit{elem, key, string(value)})
			return true
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 40 {
		t.Fatalf("hits=%d want 40", len(hits))
	}
	if hits[0].val != "u0" || hits[1].val != "0" {
		t.Fatalf("first row %v", hits[:2])
	}
	if hits[38].val != "u19" || hits[39].val != "133" {
		t.Fatalf("last row %v", hits[38:])
	}

	for i := 0; i < 20; i++ {
		u, _, _, err := Get(b, "users", "["+strconv.Itoa(i)+"]", "username")
		if err != nil || string(u) != "u"+strconv.Itoa(i) {
			t.Fatalf("Get cross-check i=%d: %s %v", i, u, err)
		}
	}
}

func TestEachArrayFieldsEarlyStop(t *testing.T) {
	data := []byte(`[{"a":1},{"a":2},{"a":3}]`)
	n := 0
	_ = EachArrayFields(data, nil, []string{"a"}, func(elem, key int, value []byte, vt ValueType, err error) bool {
		n++
		return false
	})
	if n != 1 {
		t.Fatalf("n=%d want 1", n)
	}
}
