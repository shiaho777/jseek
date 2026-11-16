package jseek

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// TestDecoderReuseZeroAlloc verifies that a pooled Decoder reused via Reset
// does not allocate a fresh bufio buffer per use (the per-request hot path).
func TestDecoderReuseZeroAlloc(t *testing.T) {
	src := strings.Repeat("{\"a\":1,\"b\":\"x\"}\n", 50)
	dec := NewNDJSONDecoder(strings.NewReader(src))
	defer dec.Close()
	dec.Reset(strings.NewReader(src))
	drain(t, dec)
	allocs := testing.AllocsPerRun(200, func() {
		dec.Reset(strings.NewReader(src))
		drain(t, dec)
	})
	// strings.NewReader allocates once per Reset; the Decoder itself must add
	// zero allocations (buffer pooled, line slices zero-copy).
	if allocs > 1 {
		t.Fatalf("reused NDJSON decoder allocated %v times/run, want <=1 (the reader)", allocs)
	}
}

func drain(t *testing.T, d *Decoder) {
	t.Helper()
	for {
		_, err := d.Next()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

// TestDecoderAutoNDJSON checks that NewDecoder auto-detects NDJSON and yields
// the same elements as the explicit NDJSON decoder.
func TestDecoderAutoNDJSON(t *testing.T) {
	src := "{\"a\":1}\n{\"a\":2}\n{\"a\":3}\n"
	auto := collect(t, src)
	explicit := NewNDJSONDecoder(strings.NewReader(src))
	var want []string
	explicit.ForEach(func(v []byte) error { want = append(want, string(v)); return nil })
	if len(auto) != len(want) {
		t.Fatalf("auto=%v want=%v", auto, want)
	}
	for i := range want {
		if auto[i] != want[i] {
			t.Errorf("elem %d: auto=%q want=%q", i, auto[i], want[i])
		}
	}
}

// TestDecoderCloseIdempotent ensures Close can be called repeatedly and Reset
// revives a closed decoder.
func TestDecoderCloseIdempotent(t *testing.T) {
	dec := NewDecoder(strings.NewReader(`[1,2,3]`))
	dec.Close()
	dec.Close()
	dec.Reset(strings.NewReader(`[4,5]`))
	var got []string
	dec.ForEach(func(v []byte) error { got = append(got, string(v)); return nil })
	if len(got) != 2 || got[0] != "4" || got[1] != "5" {
		t.Fatalf("after Close+Reset got %v", got)
	}
	dec.Close()
}

// TestDecoderPoolConcurrent exercises the pool under concurrent use.
func TestDecoderPoolConcurrent(t *testing.T) {
	src := bytes.Repeat([]byte("{\"k\":42}\n"), 100)
	done := make(chan bool, 8)
	for g := 0; g < 8; g++ {
		go func() {
			for i := 0; i < 50; i++ {
				d := NewNDJSONDecoder(bytes.NewReader(src))
				n := 0
				d.ForEach(func(v []byte) error {
					if x, _ := GetInt(v, "k"); x != 42 {
						t.Error("bad value")
					}
					n++
					return nil
				})
				d.Close()
				if n != 100 {
					t.Errorf("got %d elements", n)
				}
			}
			done <- true
		}()
	}
	for g := 0; g < 8; g++ {
		<-done
	}
}