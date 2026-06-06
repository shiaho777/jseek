package jseek

import (
	"strconv"
	"testing"
)

func makeRecords(n int) [][]byte {
	out := make([][]byte, n)
	for i := 0; i < n; i++ {
		out[i] = []byte(`{"id":` + strconv.Itoa(i) +
			`,"name":"item_` + strconv.Itoa(i) +
			`","price":` + strconv.Itoa(i) + `.5` +
			`,"active":` + strconv.FormatBool(i%2 == 0) +
			`,"meta":{"region":"r` + strconv.Itoa(i%3) + `"}}`)
	}
	return out
}

func TestTransposeInt(t *testing.T) {
	recs := makeRecords(100)
	col := TransposeInt(recs, -1, "id")
	if len(col) != 100 {
		t.Fatalf("len %d", len(col))
	}
	for i, v := range col {
		if v != int64(i) {
			t.Fatalf("row %d: got %d", i, v)
		}
	}
}

func TestTransposeFloat(t *testing.T) {
	recs := makeRecords(50)
	col := TransposeFloat(recs, -1, "price")
	for i, v := range col {
		want := float64(i) + 0.5
		if v != want {
			t.Fatalf("row %d: got %v want %v", i, v, want)
		}
	}
}

func TestTransposeString(t *testing.T) {
	recs := makeRecords(30)
	col := TransposeString(recs, "", "name")
	for i, v := range col {
		want := "item_" + strconv.Itoa(i)
		if v != want {
			t.Fatalf("row %d: got %q want %q", i, v, want)
		}
	}
}

func TestTransposeNested(t *testing.T) {
	recs := makeRecords(30)
	col := TransposeString(recs, "", "meta", "region")
	for i, v := range col {
		want := "r" + strconv.Itoa(i%3)
		if v != want {
			t.Fatalf("row %d: got %q want %q", i, v, want)
		}
	}
}

func TestTransposeBool(t *testing.T) {
	recs := makeRecords(20)
	col := TransposeBool(recs, false, "active")
	for i, v := range col {
		if v != (i%2 == 0) {
			t.Fatalf("row %d: got %v", i, v)
		}
	}
}

func TestTransposeMissing(t *testing.T) {
	recs := [][]byte{
		[]byte(`{"a":1}`),
		[]byte(`{"b":2}`),
		[]byte(`{"a":3}`),
	}
	col := TransposeInt(recs, -99, "a")
	want := []int64{1, -99, 3}
	for i := range want {
		if col[i] != want[i] {
			t.Fatalf("row %d: got %d want %d", i, col[i], want[i])
		}
	}
}

func TestTransposeMultiColumn(t *testing.T) {
	recs := makeRecords(50)
	f := Transpose(recs, []string{"id"}, []string{"name"}, []string{"meta", "region"})
	if f.Rows != 50 {
		t.Fatalf("rows %d", f.Rows)
	}
	ids := f.Int(0, -1)
	names := f.Strings(1, "")
	regions := f.Strings(2, "")
	for i := 0; i < 50; i++ {
		if ids[i] != int64(i) {
			t.Fatalf("id row %d: %d", i, ids[i])
		}
		if names[i] != "item_"+strconv.Itoa(i) {
			t.Fatalf("name row %d: %q", i, names[i])
		}
		if regions[i] != "r"+strconv.Itoa(i%3) {
			t.Fatalf("region row %d: %q", i, regions[i])
		}
	}
}

func TestTransposeHeterogeneous(t *testing.T) {
	// Mixed shapes: the cache must drift-fall-back and stay correct.
	recs := [][]byte{
		[]byte(`{"a":1,"b":2}`),
		[]byte(`{"b":20,"a":10}`),       // reordered
		[]byte(`{"x":0,"a":100,"b":3}`), // extra leading key
		[]byte(`{"a":7}`),               // b missing
	}
	a := TransposeInt(recs, -1, "a")
	b := TransposeInt(recs, -1, "b")
	wantA := []int64{1, 10, 100, 7}
	wantB := []int64{2, 20, 3, -1}
	for i := range recs {
		if a[i] != wantA[i] {
			t.Fatalf("a row %d: got %d want %d", i, a[i], wantA[i])
		}
		if b[i] != wantB[i] {
			t.Fatalf("b row %d: got %d want %d", i, b[i], wantB[i])
		}
	}
}
