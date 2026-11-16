package jseek

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidBasic(t *testing.T) {
	valid := []string{
		`{}`, `[]`, `null`, `true`, `false`, `0`, `-0`, `123`, `-123`,
		`1.5`, `-1.5e10`, `1E+2`, `1e-2`, `""`, `"abc"`, `"é"`, `"𝄞"`,
		`"A"`, `"😀"`, `"\/\\\b\f\n\r\t"`,
		`{"a":1,"b":[1,2,{"c":null}]}`,
		" \t\r\n{\"a\" : [ 1 , 2 ] } \n",
		`{"a":1,"a":2}`, // duplicate keys are legal
		`[1,2,3]`,
		// json.Valid is a syntax check: lone surrogates and raw invalid
		// UTF-8 inside strings are accepted (decoders replace them later).
		`"\uD800"`, `"\uDC00"`, `"\uD800A"`,
		"\"\xff\"", "\"\xc0\xaf\"",
		strings.Repeat("[", 100) + strings.Repeat("]", 100),
	}
	for _, s := range valid {
		if !Valid([]byte(s)) {
			t.Errorf("Valid(%q) = false, want true", s)
		}
		if !json.Valid([]byte(s)) {
			t.Fatalf("test bug: stdlib rejects valid fixture %q", s)
		}
	}
	invalid := []string{
		``, ` `, `{`, `[`, `{"a"}`, `{"a":}`, `{a:1}`, `{'a':1}`,
		`[1,]`, `[,1]`, `[1 2]`, `{"a":1,}`, `tru`, `truex`, `nulll`,
		`01`, `-`, `+1`, `.5`, `1.`, `1e`, `1e+`, `1e1.5`, `--1`,
		`"abc`, "\"\x01\"", `"\'"`, `"\u12"`, `"\uZZZZ"`,
		`{} {}`, `1 2`, `nan`, `Infinity`,
		strings.Repeat("[", 10001) + strings.Repeat("]", 10001),
	}
	for _, s := range invalid {
		if Valid([]byte(s)) {
			t.Errorf("Valid(%q) = true, want false", s)
		}
		if json.Valid([]byte(s)) {
			t.Fatalf("test bug: stdlib accepts invalid fixture %q", s)
		}
	}
}

func TestValidStringUTF8Strict(t *testing.T) {
	ok := []string{`"abc"`, `"é"`, `"𝄞"`, `"A"`, `"😀"`, `"\/\\\b\f\n\r\t"`}
	for _, s := range ok {
		if _, good := validStringUTF8([]byte(s), 0); !good {
			t.Errorf("validStringUTF8(%q) rejected valid string", s)
		}
	}
	bad := []string{
		`"\uD800"`, `"\uDC00"`, `"\uD800A"`, `"\uD800\u0041"`,
		"\"\xff\"", "\"\xc0\xaf\"", "\"\xed\xa0\x80\"", "\"\xf4\x90\x80\x80\"",
		"\"\xe0\x80\xaf\"", "\"\xf0\x80\x80\xaf\"", "\"\xc2\"",
	}
	for _, s := range bad {
		if _, good := validStringUTF8([]byte(s), 0); good {
			t.Errorf("validStringUTF8(%q) accepted invalid string", s)
		}
	}
}

func TestValidZeroAlloc(t *testing.T) {
	data := []byte(`{"users":[{"name":"Ada","tags":["a","b"],"ok":true},{"name":"Bob","tags":[],"ok":false}],"n":42}`)
	allocs := testing.AllocsPerRun(1000, func() {
		if !Valid(data) {
			panic("fixture must be valid")
		}
	})
	if allocs != 0 {
		t.Fatalf("Valid allocated %v times per run, want 0", allocs)
	}
}

// FuzzValidMatchesStdlib is the differential gate: Valid must agree with
// encoding/json.Valid on every input, valid or not.
func FuzzValidMatchesStdlib(f *testing.F) {
	seeds := []string{
		`{"a":1}`, `[1,2,3]`, `"x"`, `null`, `true`, `false`, `0`, `-1.5e3`,
		`{"a":[{},[]],"b":"é"}`, ``, `{`, `[1,]`, `01`, `"\uD800"`,
		"\"\xff\"", `{"a":1,"a":2}`, " \n\t[ 1 ] ",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		got := Valid(data)
		want := json.Valid(data)
		if got != want {
			t.Fatalf("Valid(%q) = %v, json.Valid = %v", data, got, want)
		}
	})
}