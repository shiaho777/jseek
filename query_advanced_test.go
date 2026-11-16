package jseek

import (
	"strings"
	"testing"
)

var advFixture = []byte(`{
	"name":"root",
	"a":{"name":"A","b":{"name":"B","age":40}},
	"users":[
		{"name":"Ada","age":36,"active":true},
		{"name":"Bob","age":24,"active":false},
		{"name":"Cleo","age":41,"active":true},
		{"name":"Dan","age":29,"active":true}
	],
	"nums":[10,20,30,40,50]
}`)

func collectStr(t *testing.T, path string) []string {
	t.Helper()
	res, err := Query(advFixture, path)
	if err != nil {
		t.Fatalf("Query(%q): %v", path, err)
	}
	out := make([]string, len(res))
	for i, v := range res {
		out[i] = string(v)
	}
	return out
}

func TestQueryRecursiveDescent(t *testing.T) {
	got := collectStr(t, "..name")
	want := []string{"root", "A", "B", "Ada", "Bob", "Cleo", "Dan"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("..name = %v, want %v", got, want)
	}
	// Scoped recursive descent.
	got = collectStr(t, "a..name")
	if strings.Join(got, ",") != "A,B" {
		t.Errorf("a..name = %v, want [A B]", got)
	}
	// Recursive descent continuing into a sub-path.
	got = collectStr(t, "..b.name")
	if strings.Join(got, ",") != "B" {
		t.Errorf("..b.name = %v, want [B]", got)
	}
	// First-hit mode.
	v, _, err := QueryFirst(advFixture, "..name")
	if err != nil || string(v) != "root" {
		t.Errorf("QueryFirst(..name) = %q, %v", v, err)
	}
}

func TestQuerySlice(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"nums[1:3]", "20,30"},
		{"nums[:2]", "10,20"},
		{"nums[3:]", "40,50"},
		{"nums[-2:]", "40,50"},
		{"nums[:-2]", "10,20,30"},
		{"nums[1:-1]", "20,30,40"},
		{"nums[0:99]", "10,20,30,40,50"}, // clamped
		{"nums[4:2]", ""},                 // empty
		{"users[1:3].name", "Bob,Cleo"},
	}
	for _, c := range cases {
		got := strings.Join(collectStr(t, c.path), ",")
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestQueryFilterLogic(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"users.#(age>30 && active==true)#.name", "Ada,Cleo"},
		{"users.#(age<25 || age>40)#.name", "Bob,Cleo"},
		{"users.#(active==false || age>40)#.name", "Bob,Cleo"},
		{"users.#(age>20 && age<30 && active==true)#.name", "Dan"},
		// && binds tighter than ||: (age>40) || (age<25 && active==false)
		{"users.#(age>40 || age<25 && active==false)#.name", "Bob,Cleo"},
	}
	for _, c := range cases {
		got := strings.Join(collectStr(t, c.path), ",")
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestQueryAdvancedIndexedMatches(t *testing.T) {
	doc := Index(advFixture)
	for _, p := range []string{"..name", "nums[1:3]", "users.#(age>30 && active==true)#.name"} {
		a, aerr := Query(advFixture, p)
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

func TestQueryAdvancedMalformedNoPanic(t *testing.T) {
	for _, s := range []string{`{"a":[`, `{"a":{"b":}`, `[1,2`, ``, `{`, `{"users":[{"age":}`} {
		Query([]byte(s), "..name")
		Query([]byte(s), "nums[1:3]")
		Query([]byte(s), "users.#(age>1 && active==true)#.name")
		QueryFirst([]byte(s), "..x")
	}
}

// FuzzQueryAdvancedNoPanic exercises recursive descent, slices, and compound
// filters on arbitrary bytes: it must never panic, and on valid JSON the
// three entry points (Query / QueryTyped / QueryFirst) must agree on the
// first result.
func FuzzQueryAdvancedNoPanic(f *testing.F) {
	f.Add([]byte(`{"a":{"name":"x"},"b":[{"name":"y"}]}`), "..name")
	f.Add([]byte(`[1,2,3,4,5]`), "[1:3]")
	f.Add([]byte(`{"u":[{"a":1,"b":2},{"a":3,"b":4}]}`), "u.#(a>1 && b<4)#.a")
	f.Fuzz(func(t *testing.T, data []byte, path string) {
		// Restrict to the advanced constructs to keep the fuzzer focused.
		if !strings.Contains(path, "..") && !strings.Contains(path, ":") && !strings.Contains(path, "&&") {
			return
		}
		res, err := Query(data, path)
		typed, terr := QueryTyped(data, path)
		if (err == nil) != (terr == nil) || len(res) != len(typed) {
			t.Fatalf("Query vs QueryTyped disagree: %v vs %v", err, terr)
		}
		first, _, ferr := QueryFirst(data, path)
		if len(res) > 0 {
			if ferr != nil || string(first) != string(res[0]) {
				t.Fatalf("QueryFirst=%q,%v but Query[0]=%q", first, ferr, res[0])
			}
		} else if ferr == nil {
			t.Fatalf("QueryFirst found %q but Query empty", first)
		}
	})
}