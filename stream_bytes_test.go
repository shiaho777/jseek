package jseek

import (
	"strings"
	"testing"
)

func collectBytes(t *testing.T, input string) []string {
	t.Helper()
	var out []string
	err := StreamBytes([]byte(input), func(value []byte) error {
		out = append(out, string(value))
		return nil
	})
	if err != nil {
		t.Fatalf("StreamBytes error on %q: %v", input, err)
	}
	return out
}

func TestStreamBytesArray(t *testing.T) {
	got := collectBytes(t, `[{"a":1},{"a":2},{"a":3}]`)
	want := []string{`{"a":1}`, `{"a":2}`, `{"a":3}`}
	if len(got) != 3 || got[0] != want[0] || got[2] != want[2] {
		t.Fatalf("got %v", got)
	}
}

func TestStreamBytesNDJSON(t *testing.T) {
	got := collectBytes(t, "{\"a\":1}\n{\"a\":2}\n{\"a\":3}\n")
	if len(got) != 3 {
		t.Fatalf("got %v", got)
	}
}

func TestStreamBytesScalars(t *testing.T) {
	got := collectBytes(t, `1 2.5 true null "hi"`)
	want := []string{"1", "2.5", "true", "null", `"hi"`}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("elem %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestStreamBytesMatchesDecoder(t *testing.T) {
	inputs := []string{
		`[1,2,3]`,
		`[{"a":1},{"b":[2,3]},{"c":{"d":4}}]`,
		`{"x":1}` + "\n" + `{"y":2}`,
		`"a" "b" "c"`,
		`[]`,
		`[{"s":"}],["},{"t":"a,b]c"}]`,
	}
	for _, in := range inputs {
		var fromBytes []string
		_ = StreamBytes([]byte(in), func(v []byte) error {
			fromBytes = append(fromBytes, string(v))
			return nil
		})
		var fromReader []string
		d := NewDecoder(strings.NewReader(in))
		_ = d.ForEach(func(v []byte) error {
			fromReader = append(fromReader, string(v))
			return nil
		})
		if len(fromBytes) != len(fromReader) {
			t.Errorf("%q: count bytes=%d reader=%d", in, len(fromBytes), len(fromReader))
			continue
		}
		for i := range fromBytes {
			if fromBytes[i] != fromReader[i] {
				t.Errorf("%q elem %d: bytes=%q reader=%q", in, i, fromBytes[i], fromReader[i])
			}
		}
	}
}
