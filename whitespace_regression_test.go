package jseek

import (
	"encoding/json"
	"testing"
)

// TestWhitespaceBorrowRegression guards the SWAR zero-byte borrow-chain bug:
// a 0x01 byte (e.g. '!' XOR space) next to a 0x00 byte must not be classified
// as whitespace. The bug let trailing garbage after a value pass validation
// and could mis-skip bytes during navigation.
func TestWhitespaceBorrowRegression(t *testing.T) {
	data := []byte("0      !  ")
	if got := skipWhitespace(data, 1); got != 7 {
		t.Fatalf("skipWhitespace = %d, want 7 (position of '!')", got)
	}
	if Valid(data) || json.Valid(data) {
		t.Fatal("trailing garbage must be invalid")
	}
	for _, s := range []string{"0 !", "0\t!", "0\n!", "0\r!", "1 !", "[1] !", `"x" !`, `{"a":1} !`} {
		if Valid([]byte(s)) != json.Valid([]byte(s)) {
			t.Errorf("Valid(%q) diverges from json.Valid", s)
		}
		if Valid([]byte(s)) {
			t.Errorf("Valid(%q) = true, want false", s)
		}
	}
	// Keys containing '!' adjacent to spaces in padded documents must still
	// be found by the stateless and indexed engines.
	padded := []byte(`{ "a!b" : 1 , "c" : 2 }`)
	if n, err := GetInt(padded, "a!b"); err != nil || n != 1 {
		t.Errorf(`GetInt("a!b") = %d,%v; want 1`, n, err)
	}
	doc := Index(padded)
	if n, err := doc.GetInt("c"); err != nil || n != 2 {
		t.Errorf(`doc.GetInt("c") = %d,%v; want 2`, n, err)
	}
}