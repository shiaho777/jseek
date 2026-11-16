package jseek

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestCompactIndentBasic(t *testing.T) {
	cases := []string{
		`{}`, `[]`, `null`, `true`, `42`, `-1.5e3`, `"hi"`,
		`{"a":1,"b":[1,2,{"c":null}],"d":"x"}`,
		"{\n  \"a\" : [ 1 , 2 ] ,\n \"b\" : { }\n}",
		`{"s":"}]{[","t":"a,b]c"}`,
		`{"u":"é\u0041😀"}`,
	}
	for _, s := range cases {
		data := []byte(s)
		// Compact must equal stdlib compact.
		got, err := Compact(data)
		if err != nil {
			t.Fatalf("Compact(%q): %v", s, err)
		}
		var want bytes.Buffer
		if err := json.Compact(&want, data); err != nil {
			t.Fatalf("stdlib compact(%q): %v", s, err)
		}
		if !bytes.Equal(got, want.Bytes()) {
			t.Errorf("Compact(%q) = %q, want %q", s, got, want.Bytes())
		}
		// Indent must equal stdlib indent (2 spaces).
		gotI, err := Indent(data, "  ")
		if err != nil {
			t.Fatalf("Indent(%q): %v", s, err)
		}
		var wantI bytes.Buffer
		if err := json.Indent(&wantI, data, "", "  "); err != nil {
			t.Fatalf("stdlib indent(%q): %v", s, err)
		}
		if !bytes.Equal(gotI, wantI.Bytes()) {
			t.Errorf("Indent(%q) =\n%s\nwant\n%s", s, gotI, wantI.Bytes())
		}
		// Round-trip: compact(indent(x)) == compact(x).
		rt, err := Compact(gotI)
		if err != nil || !bytes.Equal(rt, got) {
			t.Errorf("round-trip failed for %q", s)
		}
	}
}

func TestCompactIndentMalformed(t *testing.T) {
	for _, s := range []string{``, `{`, `[1,]`, `{"a":}`, `1 2`, `{"a":1}x`, `tru`, `[1`} {
		if _, err := Compact([]byte(s)); err == nil {
			t.Errorf("Compact(%q) accepted malformed input", s)
		}
		if _, err := Indent([]byte(s), "  "); err == nil {
			t.Errorf("Indent(%q) accepted malformed input", s)
		}
	}
}

func TestCompactDoesNotMutateInput(t *testing.T) {
	data := []byte("{ \"a\" : 1 }")
	orig := string(data)
	if _, err := Compact(data); err != nil {
		t.Fatal(err)
	}
	if string(data) != orig {
		t.Fatal("Compact mutated input")
	}
	if _, err := Indent(data, "\t"); err != nil {
		t.Fatal(err)
	}
	if string(data) != orig {
		t.Fatal("Indent mutated input")
	}
}

// FuzzFormatMatchesStdlib checks Compact/Indent against encoding/json on all
// valid inputs, and no-panic on everything else.
func FuzzFormatMatchesStdlib(f *testing.F) {
	seeds := []string{
		`{"a":1}`, `[1,2,3]`, `"x"`, `null`, `true`, `0`, `-1.5e3`,
		`{"a":[{},[]],"b":"é"}`, "{\n\"a\" : 1\n}", `{"s":"}{"}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if !json.Valid(data) {
			// Out of contract: only require no panic and an error.
			Compact(data)
			Indent(data, "  ")
			return
		}
		got, err := Compact(data)
		if err != nil {
			t.Fatalf("Compact rejected valid JSON %q: %v", data, err)
		}
		var want bytes.Buffer
		if err := json.Compact(&want, data); err != nil {
			t.Fatalf("stdlib compact failed on %q: %v", data, err)
		}
		if !bytes.Equal(got, want.Bytes()) {
			t.Fatalf("Compact(%q) = %q, want %q", data, got, want.Bytes())
		}
		gotI, err := Indent(data, "  ")
		if err != nil {
			t.Fatalf("Indent rejected valid JSON %q: %v", data, err)
		}
		var wantI bytes.Buffer
		if err := json.Indent(&wantI, data, "", "  "); err != nil {
			t.Fatalf("stdlib indent failed on %q: %v", data, err)
		}
		if !bytes.Equal(gotI, wantI.Bytes()) {
			t.Fatalf("Indent(%q) mismatch:\n%s\nwant\n%s", data, gotI, wantI.Bytes())
		}
	})
}

func BenchmarkCompactLarge(b *testing.B) {
	data := []byte(`{"users":[` + repeatStr(`{"name":"Ada","followers":42,"active":false},`, 499) + `{"name":"Bob","followers":7,"active":true}]}`)
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		if _, err := Compact(data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkIndentLarge(b *testing.B) {
	data := []byte(`{"users":[` + repeatStr(`{"name":"Ada","followers":42},`, 499) + `{"name":"Bob","followers":7}]}`)
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		if _, err := Indent(data, "  "); err != nil {
			b.Fatal(err)
		}
	}
}

func repeatStr(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}