package jseek

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func TestIndexTopologyStrideHomogeneous(t *testing.T) {
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i < 200; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":%d,"name":"user_%d","n":%d}`, i, i, i*3)
	}
	b.WriteByte(']')
	data := []byte(b.String())
	d := Index(data)
	for _, n := range []int{0, 1, 2, 9, 50, 99, 150, 199} {
		v, err := d.GetString("["+strconv.Itoa(n)+"]", "name")
		if err != nil || v != "user_"+strconv.Itoa(n) {
			t.Fatalf("n=%d name=%q err=%v", n, v, err)
		}
		id, err := d.GetInt("["+strconv.Itoa(n)+"]", "id")
		if err != nil || id != int64(n) {
			t.Fatalf("n=%d id=%d err=%v", n, id, err)
		}
	}
}

func TestIndexTopologyStrideNested(t *testing.T) {
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i < 80; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":%d,"items":[{"x":1},{"x":2},{"x":3}],"tail":%d}`, i, i*10)
	}
	b.WriteByte(']')
	data := []byte(b.String())
	d := Index(data)
	for _, n := range []int{0, 1, 5, 40, 79} {
		id, err := d.GetInt("["+strconv.Itoa(n)+"]", "id")
		if err != nil || id != int64(n) {
			t.Fatalf("n=%d id=%d err=%v", n, id, err)
		}
		x, err := d.GetInt("["+strconv.Itoa(n)+"]", "items", "[1]", "x")
		if err != nil || x != 2 {
			t.Fatalf("n=%d x=%d err=%v", n, x, err)
		}
		tail, err := d.GetInt("["+strconv.Itoa(n)+"]", "tail")
		if err != nil || tail != int64(n*10) {
			t.Fatalf("n=%d tail=%d err=%v", n, tail, err)
		}
	}
}

func TestIndexTopologyStrideDigitGrowth(t *testing.T) {
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i < 150; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":%d,"n":%d}`, i, i*7)
	}
	b.WriteByte(']')
	data := []byte(b.String())
	d := Index(data)
	for _, n := range []int{0, 1, 9, 10, 99, 100, 149} {
		id, err := d.GetInt("["+strconv.Itoa(n)+"]", "id")
		if err != nil || id != int64(n) {
			t.Fatalf("n=%d id=%d err=%v", n, id, err)
		}
		nv, err := d.GetInt("["+strconv.Itoa(n)+"]", "n")
		if err != nil || nv != int64(n*7) {
			t.Fatalf("n=%d n=%d err=%v", n, nv, err)
		}
	}
}

func TestIndexTopologyStrideWithTape(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"users":[`)
	for i := 0; i < 100; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":%d,"active":true}`, i)
	}
	b.WriteString(`]}`)
	data := []byte(b.String())
	d := Index(data).WithTape()
	for _, n := range []int{0, 1, 50, 99} {
		id, err := d.GetInt("users", "["+strconv.Itoa(n)+"]", "id")
		if err != nil || id != int64(n) {
			t.Fatalf("n=%d id=%d err=%v", n, id, err)
		}
	}
}

func TestIndexTopologyStrideMixedLengthsFallback(t *testing.T) {
	data := []byte(`[{"a":1},{"a":1,"b":2},{"a":1},{"a":1,"b":2,"c":3},{"a":1}]`)
	d := Index(data)
	for _, n := range []int{0, 1, 2, 3, 4} {
		v, err := d.GetInt("["+strconv.Itoa(n)+"]", "a")
		if err != nil || v != 1 {
			t.Fatalf("n=%d a=%d err=%v", n, v, err)
		}
	}
	if v, err := d.GetInt("[1]", "b"); err != nil || v != 2 {
		t.Fatalf("b=%d err=%v", v, err)
	}
	if _, err := d.GetInt("[0]", "b"); err == nil {
		t.Fatal("expected missing")
	}
}
