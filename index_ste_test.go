package jseek

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func TestSTEMatchesStatelessLarge(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"meta":{"v":1},"users":[`)
	for i := 0; i < 120; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":%d,"name":"user_%d","n":%d,"nested":{"x":%d}}`, i, i, i*3, i+7)
	}
	b.WriteString(`],"tail":true}`)
	data := []byte(b.String())
	d := Index(data)
	paths := [][]string{
		{"meta", "v"},
		{"tail"},
		{"users", "[0]", "id"},
		{"users", "[1]", "name"},
		{"users", "[9]", "n"},
		{"users", "[10]", "id"},
		{"users", "[50]", "nested", "x"},
		{"users", "[99]", "name"},
		{"users", "[119]", "id"},
	}
	for _, p := range paths {
		wv, wt, _, werr := Get(data, p...)
		gv, gt, _, gerr := d.Get(p...)
		if (werr == nil) != (gerr == nil) || wt != gt || string(wv) != string(gv) {
			t.Fatalf("path %v: stateless=(%v,%q,%v) indexed=(%v,%q,%v)", p, wt, wv, werr, gt, gv, gerr)
		}
	}
}

func TestSTENestedObjectArrays(t *testing.T) {
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i < 40; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":%d,"items":[{"x":1},{"x":2},{"x":3}],"tail":%d}`, i, i*10)
	}
	b.WriteByte(']')
	data := []byte(b.String())
	d := Index(data).WithTape()
	for _, n := range []int{0, 1, 15, 39} {
		id, err := d.GetInt("["+strconv.Itoa(n)+"]", "id")
		if err != nil || id != int64(n) {
			t.Fatalf("id n=%d: %d %v", n, id, err)
		}
		x, err := d.GetInt("["+strconv.Itoa(n)+"]", "items", "[2]", "x")
		if err != nil || x != 3 {
			t.Fatalf("x n=%d: %d %v", n, x, err)
		}
	}
}

func TestSTEWhitespaceArray(t *testing.T) {
	data := []byte("[\n  {\"a\":1},\n  {\"a\":2},\n  {\"a\":3}\n]")
	d := Index(data)
	for n := 0; n < 3; n++ {
		v, err := d.GetInt("["+strconv.Itoa(n)+"]", "a")
		if err != nil || v != int64(n+1) {
			t.Fatalf("n=%d v=%d err=%v", n, v, err)
		}
	}
}
