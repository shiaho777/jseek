package jseek

import (
	"errors"
	"strings"
	"testing"
)

func TestPathErrorNotFoundIsSentinel(t *testing.T) {
	_, err := At[string](sample, "person", "missing", "deep")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrKeyPathNotFound) {
		t.Fatalf("errors.Is(ErrKeyPathNotFound) failed for %v", err)
	}
	var pe *PathError
	if !errors.As(err, &pe) {
		t.Fatalf("errors.As(*PathError) failed for %v", err)
	}
	// It should report which segment failed.
	if pe.At != 1 {
		t.Errorf("expected failure at segment 1, got %d", pe.At)
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("message should mention the failing key: %q", err.Error())
	}
}

func TestPathErrorTypeMismatch(t *testing.T) {
	// score is a float; asking for an int should be a type error with context.
	_, err := At[int64](sample, "person", "score")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrUnexpectedType) {
		t.Fatalf("errors.Is(ErrUnexpectedType) failed for %v", err)
	}
	var pe *PathError
	if !errors.As(err, &pe) {
		t.Fatalf("errors.As(*PathError) failed")
	}
	if pe.Got != Number || pe.Want != Number {
		t.Errorf("got/want types: %v/%v", pe.Got, pe.Want)
	}
	msg := err.Error()
	if !strings.Contains(msg, "score") {
		t.Errorf("message should mention path: %q", msg)
	}
}

func TestFormatPath(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"a", "b", "c"}, "a.b.c"},
		{[]string{"a", "[0]", "b"}, "a[0].b"},
		{nil, "(root)"},
	}
	for _, c := range cases {
		if got := formatPath(c.in); got != c.want {
			t.Errorf("formatPath(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
