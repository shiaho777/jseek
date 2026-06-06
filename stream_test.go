package jseek

import (
	"io"
	"strings"
	"testing"
)

func collect(t *testing.T, input string) []string {
	t.Helper()
	d := NewDecoder(strings.NewReader(input))
	var out []string
	err := d.ForEach(func(value []byte) error {
		out = append(out, string(value))
		return nil
	})
	if err != nil {
		t.Fatalf("ForEach error on %q: %v", input, err)
	}
	return out
}

func TestStreamArray(t *testing.T) {
	got := collect(t, `[{"a":1},{"a":2},{"a":3}]`)
	want := []string{`{"a":1}`, `{"a":2}`, `{"a":3}`}
	if len(got) != len(want) {
		t.Fatalf("got %d elements: %v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("elem %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestStreamArrayWithWhitespace(t *testing.T) {
	got := collect(t, "[\n  {\"a\": 1},\n  {\"a\": 2}\n]")
	if len(got) != 2 {
		t.Fatalf("got %v", got)
	}
}

func TestStreamNDJSON(t *testing.T) {
	got := collect(t, "{\"a\":1}\n{\"a\":2}\n{\"a\":3}\n")
	if len(got) != 3 {
		t.Fatalf("got %d: %v", len(got), got)
	}
}

func TestStreamScalars(t *testing.T) {
	got := collect(t, "1 2.5 true null \"hi\"")
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

func TestStreamNestedAndStringsWithBraces(t *testing.T) {
	// Braces and brackets inside strings must not confuse depth tracking.
	got := collect(t, `[{"s":"}]{["},{"t":"a,b]c"}]`)
	want := []string{`{"s":"}]{["}`, `{"t":"a,b]c"}`}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestStreamEmptyArray(t *testing.T) {
	got := collect(t, `[]`)
	if len(got) != 0 {
		t.Fatalf("expected no elements, got %v", got)
	}
}

func TestStreamElementsAreQueryable(t *testing.T) {
	d := NewDecoder(strings.NewReader(`[{"user":{"name":"Ada"}},{"user":{"name":"Bob"}}]`))
	var names []string
	err := d.ForEach(func(value []byte) error {
		n, err := GetString(value, "user", "name")
		if err != nil {
			return err
		}
		names = append(names, n)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "Ada" || names[1] != "Bob" {
		t.Fatalf("got %v", names)
	}
}

func TestStreamElementsIndexable(t *testing.T) {
	// Each streamed element should work with the indexed engine too.
	d := NewDecoder(strings.NewReader(`{"x":[1,2,3]}` + "\n" + `{"x":[4,5,6]}`))
	var sums []int64
	for {
		v, err := d.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		doc := Index(v)
		a, _ := doc.GetInt("x", "[0]")
		c, _ := doc.GetInt("x", "[2]")
		sums = append(sums, a+c)
	}
	if len(sums) != 2 || sums[0] != 4 || sums[1] != 10 {
		t.Fatalf("got %v", sums)
	}
}

func TestStreamTooLarge(t *testing.T) {
	d := NewDecoder(strings.NewReader(`["` + strings.Repeat("x", 1000) + `"]`))
	d.MaxValue = 100
	_, err := d.Next()
	if err != ErrTooLarge {
		t.Fatalf("expected ErrTooLarge, got %v", err)
	}
}

func TestStreamTruncatedContainer(t *testing.T) {
	d := NewDecoder(strings.NewReader(`[{"a":1`))
	_, err := d.Next()
	if err == nil {
		t.Fatal("expected error on truncated input")
	}
}
