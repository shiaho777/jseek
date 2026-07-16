package jseek

import "testing"

func TestStreamNDJSONLines(t *testing.T) {
	in := []byte("{\"a\":1}\n{\"a\":2}\r\n\n{\"a\":3}\n")
	var got []string
	err := StreamNDJSON(in, func(v []byte) error {
		got = append(got, string(v))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != `{"a":1}` || got[2] != `{"a":3}` {
		t.Fatalf("got %v", got)
	}
}

func TestStreamNDJSONEachPaths(t *testing.T) {
	in := []byte("{\"x\":1,\"y\":2}\n{\"x\":3,\"y\":4}\n")
	q := CompileStrings([]string{"y"}, []string{"x"})
	var ys []string
	err := StreamNDJSONEach(in, q, func(idx int, value []byte, vt ValueType, err error) error {
		if idx == 0 {
			ys = append(ys, string(value))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ys) != 2 || ys[0] != "2" || ys[1] != "4" {
		t.Fatalf("ys=%v", ys)
	}
}

func TestEachEarlyExitSkipsTrailing(t *testing.T) {
	long := make([]byte, 0, 200)
	long = append(long, `{"a":1,"b":2,"huge":"`...)
	for i := 0; i < 4000; i++ {
		long = append(long, 'x')
	}
	long = append(long, `"}`...)
	q := CompileStrings([]string{"a"}, []string{"b"})
	hits := 0
	q.Each(long, func(idx int, value []byte, vt ValueType, err error) {
		hits++
	})
	if hits != 2 {
		t.Fatalf("hits=%d", hits)
	}
}
