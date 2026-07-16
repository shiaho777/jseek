package jseek

import (
	"bytes"
	"io"
	"strconv"
	"testing"
)

func TestNDJSONDecoder(t *testing.T) {
	in := []byte("{\"a\":1}\n\n{\"a\":2}\r\n{\"a\":3}\n")
	d := NewNDJSONDecoder(bytes.NewReader(in))
	var got []string
	for {
		v, err := d.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, string(v))
	}
	if len(got) != 3 || got[0] != "{\"a\":1}" || got[2] != "{\"a\":3}" {
		t.Fatalf("got %v", got)
	}
}

func TestNDJSONDecoderForEach(t *testing.T) {
	var buf bytes.Buffer
	for i := 0; i < 100; i++ {
		buf.WriteString("{\"i\":")
		buf.WriteString(strconv.Itoa(i))
		buf.WriteString("}\n")
	}
	var n int
	err := NewNDJSONDecoder(bytes.NewReader(buf.Bytes())).ForEach(func(v []byte) error {
		n++
		if len(v) == 0 || v[0] != '{' {
			t.Fatalf("bad %q", v)
		}
		return nil
	})
	if err != nil || n != 100 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestNDJSONDecoderReset(t *testing.T) {
	d := NewNDJSONDecoder(bytes.NewReader([]byte("{\"a\":1}\n")))
	v, err := d.Next()
	if err != nil || string(v) != "{\"a\":1}" {
		t.Fatalf("first %q %v", v, err)
	}
	d.Reset(bytes.NewReader([]byte("{\"a\":2}\n{\"a\":3}\n")))
	var n int
	_ = d.ForEach(func(v []byte) error {
		n++
		return nil
	})
	if n != 2 {
		t.Fatalf("n=%d", n)
	}
}
