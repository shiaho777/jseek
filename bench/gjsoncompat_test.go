package bench

import (
	"bytes"
	"testing"

	gjson "github.com/shiaho777/jseek/gjsoncompat"
	realgjson "github.com/tidwall/gjson"
)

// TestGjsonCompatMatchesReal checks the drop-in layer against real gjson on the
// common single-value API surface.
func TestGjsonCompatMatchesReal(t *testing.T) {
	docs := []string{
		`{"name":"Ada","age":36,"active":true,"tags":["a","b"],"addr":{"city":"NYC"}}`,
		`{"users":[{"id":1,"name":"Bob"},{"id":2,"name":"Cleo"}]}`,
		`{"n":null,"f":1.5,"neg":-3}`,
		`[1,2,3]`,
		`{"a":{"b":{"c":"deep"}}}`,
	}
	paths := []string{
		"name", "age", "active", "tags.0", "addr.city", "n", "f", "neg",
		"users.0.name", "users.1.id", "a.b.c", "missing", "users.#",
	}
	for _, doc := range docs {
		for _, p := range paths {
			want := realgjson.Get(doc, p)
			got := gjson.Get(doc, p)
			if got.Exists() != want.Exists() {
				t.Errorf("doc=%s path=%s exists: got=%v want=%v", doc, p, got.Exists(), want.Exists())
				continue
			}
			if !want.Exists() {
				continue
			}
			if got.Type != gjson.Type(want.Type) {
				t.Errorf("doc=%s path=%s type: got=%v want=%v", doc, p, got.Type, want.Type)
			}
			switch want.Type {
			case realgjson.String:
				if got.Str != want.Str {
					t.Errorf("doc=%s path=%s str: got=%q want=%q", doc, p, got.Str, want.Str)
				}
			case realgjson.Number:
				if got.Num != want.Num {
					t.Errorf("doc=%s path=%s num: got=%v want=%v", doc, p, got.Num, want.Num)
				}
			case realgjson.True, realgjson.False:
				if got.Bool() != want.Bool() {
					t.Errorf("doc=%s path=%s bool mismatch", doc, p)
				}
			}
		}
	}
}

func TestGjsonCompatValid(t *testing.T) {
	for _, s := range []string{`{"a":1}`, `[1,2]`, `"x"`, `null`, `{bad`, ``, `01`} {
		if gjson.Valid(s) != realgjson.Valid(s) {
			t.Errorf("Valid(%q): got=%v want=%v", s, gjson.Valid(s), realgjson.Valid(s))
		}
	}
}

func TestGjsonCompatGetMany(t *testing.T) {
	doc := `{"a":1,"b":"x","c":true}`
	paths := []string{"a", "b", "c", "missing"}
	got := gjson.GetMany(doc, paths...)
	want := realgjson.GetMany(doc, paths...)
	if len(got) != len(want) {
		t.Fatalf("len mismatch")
	}
	for i := range got {
		if got[i].Exists() != want[i].Exists() {
			t.Errorf("path %s exists mismatch", paths[i])
		}
	}
}

// FuzzGjsonCompatSinglePath checks single-key lookups agree with real gjson on
// arbitrary valid JSON.
func FuzzGjsonCompatSinglePath(f *testing.F) {
	f.Add([]byte(`{"a":1,"b":"x","c":true,"d":null}`), "a")
	f.Add([]byte(`{"nested":{"k":[1,2]}}`), "nested")
	f.Fuzz(func(t *testing.T, data []byte, key string) {
		if !realgjson.ValidBytes(data) || key == "" {
			return
		}
		// The compat layer targets plain object-key lookups on object documents
		// (the real gjson migration case). Restrict to object roots and simple
		// alphanumeric keys; gjson's modifier/syntax quirks on scalar roots are
		// out of scope (documented).
		trimmed := bytes.TrimSpace(data)
		if len(trimmed) == 0 || trimmed[0] != '{' {
			return
		}
		for i := 0; i < len(key); i++ {
			c := key[i]
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
				return
			}
		}
		want := realgjson.GetBytes(data, key)
		got := gjson.GetBytes(data, key)
		if got.Exists() != want.Exists() {
			t.Fatalf("key=%q exists got=%v want=%v data=%s", key, got.Exists(), want.Exists(), data)
		}
		if !want.Exists() {
			return
		}
		if got.Type != gjson.Type(want.Type) {
			t.Fatalf("key=%q type got=%v want=%v", key, got.Type, want.Type)
		}
		if want.Type == realgjson.String && got.Str != want.Str {
			t.Fatalf("key=%q str got=%q want=%q", key, got.Str, want.Str)
		}
		if want.Type == realgjson.Number && got.Num != want.Num {
			t.Fatalf("key=%q num got=%v want=%v", key, got.Num, want.Num)
		}
	})
}

func TestGjsonCompatArrayMap(t *testing.T) {
	doc := `{"arr":[1,2,3],"obj":{"x":1,"y":2}}`
	arr := gjson.Get(doc, "arr").Array()
	if len(arr) != 3 || arr[0].Int() != 1 || arr[2].Int() != 3 {
		t.Errorf("Array: %v", arr)
	}
	m := gjson.Get(doc, "obj").Map()
	if len(m) != 2 || m["x"].Int() != 1 || m["y"].Int() != 2 {
		t.Errorf("Map: %v", m)
	}
}
