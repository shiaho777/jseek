package jseek

import (
	"bytes"
	"testing"
)

// AppendSet/AppendDelete must produce byte-identical results to Set/Delete, and
// must be amortized zero-allocation when a scratch buffer is reused.

func TestAppendSetMatchesSet(t *testing.T) {
	cases := []struct {
		data string
		val  string
		keys []string
	}{
		{`{"a":{"b":1},"c":2}`, `99`, []string{"a", "b"}},          // replace existing
		{`{"a":{"b":1}}`, `42`, []string{"a", "x"}},               // create missing key
		{`{}`, `7`, []string{"x", "y", "z"}},                      // build nested into empty obj
		{`{"arr":[10,20,30]}`, `0`, []string{"arr", "[1]"}},       // replace array element
		{`  {"a":1}  `, `"s"`, []string{"a"}},                     // surrounding whitespace
		{`{"a":1}`, `{"deep":true}`, []string{"b"}},               // insert object value
	}
	for _, c := range cases {
		want, err := Set([]byte(c.data), []byte(c.val), c.keys...)
		if err != nil {
			t.Fatalf("Set(%q) error: %v", c.data, err)
		}
		// Reuse a buffer with junk in it to prove AppendSet truncates correctly
		// via the caller's slicing, not by relying on an empty dst.
		buf := []byte("PREFIX")
		buf, err = AppendSet(buf[:0], []byte(c.data), []byte(c.val), c.keys...)
		if err != nil {
			t.Fatalf("AppendSet(%q) error: %v", c.data, err)
		}
		if !bytes.Equal(buf, want) {
			t.Errorf("AppendSet(%q,%q)=%q, want %q", c.data, c.val, buf, want)
		}
	}
}

func TestAppendDeleteMatchesDelete(t *testing.T) {
	cases := []struct {
		data string
		keys []string
	}{
		{`{"a":1,"b":2,"c":3}`, []string{"b"}},      // middle member
		{`{"a":1,"b":2}`, []string{"a"}},            // first member
		{`{"a":1}`, []string{"a"}},                  // only member
		{`{"x":{"y":1,"z":2}}`, []string{"x", "y"}}, // nested
		{`{"arr":[1,2,3]}`, []string{"arr", "[1]"}}, // array element
		{`{"a":1}`, []string{"missing"}},            // not found -> identical copy
	}
	for _, c := range cases {
		want := Delete([]byte(c.data), c.keys...)
		buf, _ := AppendDelete(make([]byte, 0, 4), []byte(c.data), c.keys...)
		if !bytes.Equal(buf, want) {
			t.Errorf("AppendDelete(%q,%v)=%q, want %q", c.data, c.keys, buf, want)
		}
	}
}

func TestAppendSetNeverAliasesInput(t *testing.T) {
	data := []byte(`{"a":1}`)
	out, err := AppendSet(nil, data, []byte(`2`), "a")
	if err != nil {
		t.Fatal(err)
	}
	out[0] = 'X' // mutate result
	if data[0] != '{' {
		t.Fatal("AppendSet result aliased the input")
	}
}

func TestAppendSetZeroAllocOnReuse(t *testing.T) {
	data := []byte(`{"user":{"name":"old","age":30},"active":true}`)
	val := []byte(`"new"`)
	buf := make([]byte, 0, 256) // pre-sized so reuse needs no growth
	allocs := testing.AllocsPerRun(1000, func() {
		var err error
		buf, err = AppendSet(buf[:0], data, val, "user", "name")
		if err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("AppendSet with reused buffer allocated %v times/op, want 0", allocs)
	}
}

func TestAppendDeleteZeroAllocOnReuse(t *testing.T) {
	data := []byte(`{"a":1,"b":2,"c":3,"d":4}`)
	buf := make([]byte, 0, 256)
	allocs := testing.AllocsPerRun(1000, func() {
		buf, _ = AppendDelete(buf[:0], data, "c")
	})
	if allocs != 0 {
		t.Fatalf("AppendDelete with reused buffer allocated %v times/op, want 0", allocs)
	}
}

// Deeply nested replacement: the old recursive Set allocated once per level;
// AppendSet into a reused buffer allocates zero regardless of depth.
func TestAppendSetDeepZeroAlloc(t *testing.T) {
	data := []byte(`{"l1":{"l2":{"l3":{"l4":{"l5":"deep"}}}}}`)
	val := []byte(`"x"`)
	buf := make([]byte, 0, 256)
	allocs := testing.AllocsPerRun(1000, func() {
		buf, _ = AppendSet(buf[:0], data, val, "l1", "l2", "l3", "l4", "l5")
	})
	if allocs != 0 {
		t.Fatalf("deep AppendSet allocated %v times/op, want 0", allocs)
	}
}

func BenchmarkSetAlloc(b *testing.B) {
	data := []byte(`{"user":{"name":"old","age":30},"active":true}`)
	val := []byte(`"new"`)
	b.Run("Set", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = Set(data, val, "user", "name")
		}
	})
	b.Run("AppendSet_reuse", func(b *testing.B) {
		buf := make([]byte, 0, 256)
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			buf, _ = AppendSet(buf[:0], data, val, "user", "name")
		}
	})
}
