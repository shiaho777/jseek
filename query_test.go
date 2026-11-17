package jseek

import (
	"errors"
	"strings"
	"testing"
)

var queryFixture = []byte(`{"users":[
	{"name":"Ada","age":36,"active":true,"tags":["admin","dev"]},
	{"name":"Bob","age":24,"active":false,"tags":["dev"]},
	{"name":"Cleo","age":41,"active":true,"tags":[]},
	{"name":"Dan","age":24,"active":true}
],"meta":{"region":"eu","limits":{"rps":100}}}`)

func TestQueryWildcard(t *testing.T) {
	for _, path := range []string{"users.#.name", "users.*.name"} {
		got, err := Query(queryFixture, path)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"Ada", "Bob", "Cleo", "Dan"}
		if len(got) != len(want) {
			t.Fatalf("%s: got %d results, want %d", path, len(got), len(want))
		}
		for i, w := range want {
			if string(got[i]) != w {
				t.Errorf("%s[%d] = %q, want %q", path, i, got[i], w)
			}
		}
	}
	// "*" also iterates object members (jseek extension).
	got, err := Query(queryFixture, "meta.*")
	if err != nil || len(got) != 2 {
		t.Fatalf("meta.* = %v, %v; want 2 results", got, err)
	}
	// Nested wildcard.
	got, err = Query(queryFixture, "users.#.tags.*")
	if err != nil || len(got) != 3 {
		t.Fatalf("tags.* = %v, %v; want 3 results", got, err)
	}
	// Missing paths yield empty, not error.
	got, err = Query(queryFixture, "nope.#.name")
	if err != nil || len(got) != 0 {
		t.Fatalf("missing path = %v, %v; want empty", got, err)
	}
}

func TestQueryFilter(t *testing.T) {
	cases := []struct {
		path string
		want []string
	}{
		{"users.#(age>30).name", []string{"Ada"}},          // first match (gjson)
		{"users.#(age>30)#.name", []string{"Ada", "Cleo"}}, // all matches
		{"users.#(age==24)#.name", []string{"Bob", "Dan"}},
		{"users.#(active==true)#.name", []string{"Ada", "Cleo", "Dan"}},
		{"users.#(name==\"Bob\").age", []string{"24"}},
		{"users.#(tags)#.name", []string{"Ada", "Bob", "Cleo"}}, // existence
		{"users.#(missing>0).name", nil},
	}
	for _, c := range cases {
		got, err := Query(queryFixture, c.path)
		if err != nil {
			t.Fatalf("%s: %v", c.path, err)
		}
		if len(got) != len(c.want) {
			t.Fatalf("%s: got %d results %q, want %d", c.path, len(got), got, len(c.want))
		}
		for i := range c.want {
			if string(got[i]) != c.want[i] {
				t.Errorf("%s[%d] = %q, want %q", c.path, i, got[i], c.want[i])
			}
		}
	}
	// Filter yielding whole elements (all matches).
	got, err := Query(queryFixture, "users.#(age>30)#")
	if err != nil || len(got) != 2 || !strings.Contains(string(got[0]), `"Ada"`) {
		t.Fatalf("element filter = %v, %v", got, err)
	}
}

func TestQueryFirst(t *testing.T) {
	v, vt, err := QueryFirst(queryFixture, "users.#(age>30).name")
	if err != nil || string(v) != "Ada" || vt != String {
		t.Fatalf("QueryFirst = %q,%v,%v; want Ada,String", v, vt, err)
	}
	_, _, err = QueryFirst(queryFixture, "users.#(age>100).name")
	if !errors.Is(err, ErrKeyPathNotFound) {
		t.Fatalf("no match err = %v, want ErrKeyPathNotFound", err)
	}
}

func TestQueryLiteralCompat(t *testing.T) {
	// Without wildcards, Query must agree with GetPath.
	for _, p := range []string{"meta.region", "meta.limits.rps", "users[1].name", "users.0.age"} {
		q, err := Query(queryFixture, p)
		g, _, _, gerr := GetPath(queryFixture, p)
		if (err == nil) != (gerr == nil) {
			t.Fatalf("%s: Query err=%v GetPath err=%v", p, err, gerr)
		}
		if err == nil && (len(q) != 1 || string(q[0]) != string(g)) {
			t.Errorf("%s: Query=%q GetPath=%q", p, q, g)
		}
	}
	// Trailing count matches Count.
	q, err := Query(queryFixture, "users.#")
	if err != nil || len(q) != 1 || string(q[0]) != "4" {
		t.Fatalf("users.# = %q, %v; want [4]", q, err)
	}
}

func TestQueryIndexedMatches(t *testing.T) {
	doc := Index(queryFixture)
	for _, p := range []string{"users.#.name", "users.#(age>30)#.name", "meta.*"} {
		a, aerr := Query(queryFixture, p)
		b, berr := doc.Query(p)
		if (aerr == nil) != (berr == nil) || len(a) != len(b) {
			t.Fatalf("%s: stateless=%d,%v indexed=%d,%v", p, len(a), aerr, len(b), berr)
		}
		for i := range a {
			if string(a[i]) != string(b[i]) {
				t.Errorf("%s[%d]: %q vs %q", p, i, a[i], b[i])
			}
		}
	}
}

func TestQueryMalformedNoPanic(t *testing.T) {
	for _, s := range []string{`{"users":[`, `{"users":[{"a":}`, `[1,2`, ``, `{`} {
		Query([]byte(s), "users.*.name")
		Query([]byte(s), "users.#(a>1).b")
		QueryFirst([]byte(s), "*")
	}
}

func BenchmarkQueryWildcard(b *testing.B) {
	data := []byte(`{"users":[` + strings.Repeat(`{"name":"Ada","followers":42,"active":true},`, 499) + `{"name":"Bob","followers":7,"active":false}]}`)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		out, err := Query(data, "users.*.name")
		if err != nil || len(out) != 500 {
			b.Fatal("bad query")
		}
	}
}

func BenchmarkQueryFilter(b *testing.B) {
	data := []byte(`{"users":[` + strings.Repeat(`{"name":"Ada","followers":42,"active":true},`, 499) + `{"name":"Bob","followers":7,"active":false}]}`)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		out, err := Query(data, "users.#(followers>10).name")
		if err != nil || len(out) != 499 {
			b.Fatal("bad query")
		}
	}
}