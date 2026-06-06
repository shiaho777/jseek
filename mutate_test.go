package jseek

import (
	"encoding/json"
	"reflect"
	"testing"
)

// jsonEqual compares two JSON documents for semantic equality (ignoring
// formatting and key order).
func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		t.Fatalf("left not valid JSON: %v (%s)", err, a)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		t.Fatalf("right not valid JSON: %v (%s)", err, b)
	}
	return reflect.DeepEqual(av, bv)
}

func TestSetReplaceExisting(t *testing.T) {
	data := []byte(`{"a":1,"b":2}`)
	out, err := Set(data, []byte("42"), "a")
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(t, out, []byte(`{"a":42,"b":2}`)) {
		t.Fatalf("got %s", out)
	}
	// Input must be untouched.
	if string(data) != `{"a":1,"b":2}` {
		t.Fatalf("input mutated: %s", data)
	}
}

func TestSetNested(t *testing.T) {
	data := []byte(`{"a":{"b":{"c":1}}}`)
	out, err := Set(data, []byte(`"x"`), "a", "b", "c")
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(t, out, []byte(`{"a":{"b":{"c":"x"}}}`)) {
		t.Fatalf("got %s", out)
	}
}

func TestSetCreatesKey(t *testing.T) {
	data := []byte(`{"a":1}`)
	out, err := Set(data, []byte("true"), "b")
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(t, out, []byte(`{"a":1,"b":true}`)) {
		t.Fatalf("got %s", out)
	}
}

func TestSetCreatesNestedPath(t *testing.T) {
	data := []byte(`{"a":1}`)
	out, err := Set(data, []byte(`"deep"`), "x", "y", "z")
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(t, out, []byte(`{"a":1,"x":{"y":{"z":"deep"}}}`)) {
		t.Fatalf("got %s", out)
	}
}

func TestSetIntoEmptyObject(t *testing.T) {
	data := []byte(`{}`)
	out, err := Set(data, []byte("1"), "a")
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(t, out, []byte(`{"a":1}`)) {
		t.Fatalf("got %s", out)
	}
}

func TestSetArrayElement(t *testing.T) {
	data := []byte(`{"a":[10,20,30]}`)
	out, err := Set(data, []byte("99"), "a", "[1]")
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(t, out, []byte(`{"a":[10,99,30]}`)) {
		t.Fatalf("got %s", out)
	}
}

func TestDeleteObjectMember(t *testing.T) {
	cases := []struct {
		in, key, want string
	}{
		{`{"a":1,"b":2,"c":3}`, "a", `{"b":2,"c":3}`},
		{`{"a":1,"b":2,"c":3}`, "b", `{"a":1,"c":3}`},
		{`{"a":1,"b":2,"c":3}`, "c", `{"a":1,"b":2}`},
		{`{"a":1}`, "a", `{}`},
	}
	for _, c := range cases {
		out := Delete([]byte(c.in), c.key)
		if !jsonEqual(t, out, []byte(c.want)) {
			t.Errorf("Delete(%s, %q) = %s, want %s", c.in, c.key, out, c.want)
		}
	}
}

func TestDeleteNested(t *testing.T) {
	data := []byte(`{"a":{"b":1,"c":2}}`)
	out := Delete(data, "a", "b")
	if !jsonEqual(t, out, []byte(`{"a":{"c":2}}`)) {
		t.Fatalf("got %s", out)
	}
}

func TestDeleteArrayElement(t *testing.T) {
	cases := []struct {
		in, idx, want string
	}{
		{`[10,20,30]`, "[0]", `[20,30]`},
		{`[10,20,30]`, "[1]", `[10,30]`},
		{`[10,20,30]`, "[2]", `[10,20]`},
	}
	for _, c := range cases {
		out := Delete([]byte(c.in), c.idx)
		if !jsonEqual(t, out, []byte(c.want)) {
			t.Errorf("Delete(%s, %q) = %s, want %s", c.in, c.idx, out, c.want)
		}
	}
}

func TestDeleteMissing(t *testing.T) {
	data := []byte(`{"a":1}`)
	out := Delete(data, "nope")
	if !jsonEqual(t, out, data) {
		t.Fatalf("got %s", out)
	}
}

func TestSetThenGet(t *testing.T) {
	data := []byte(`{"user":{"name":"old"}}`)
	out, err := Set(data, []byte(`"new"`), "user", "name")
	if err != nil {
		t.Fatal(err)
	}
	got, err := GetString(out, "user", "name")
	if err != nil || got != "new" {
		t.Fatalf("round-trip failed: %q %v", got, err)
	}
}

func TestGenericAt(t *testing.T) {
	s, err := At[string](sample, "person", "name", "fullName")
	if err != nil || s != "Leonid Bugaev" {
		t.Fatalf("At[string]: %q %v", s, err)
	}
	n, err := At[int64](sample, "person", "github", "followers")
	if err != nil || n != 109 {
		t.Fatalf("At[int64]: %d %v", n, err)
	}
	f, err := At[float64](sample, "person", "score")
	if err != nil || f != 98.6 {
		t.Fatalf("At[float64]: %v %v", f, err)
	}
	b, err := At[bool](sample, "person", "active")
	if err != nil || !b {
		t.Fatalf("At[bool]: %v %v", b, err)
	}
}

func TestGenericOr(t *testing.T) {
	if got := Or[int64](sample, -1, "missing"); got != -1 {
		t.Fatalf("Or fallback: got %d", got)
	}
	if got := Or[int64](sample, -1, "person", "github", "followers"); got != 109 {
		t.Fatalf("Or hit: got %d", got)
	}
}
