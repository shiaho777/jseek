package jseek

import (
	"bytes"
	"testing"
)

// These regression tests pin down a correctness bug found by differential
// fuzzing: the trajectory cache (Pin and the former multi-column Transpose
// cache) remembered a value's structural-index position learned on one record
// and replayed it on another. Under duplicate keys, that position could align
// with a LATER duplicate that still passed a key check, so the cache returned a
// non-first occurrence while jseek's contract (and stateless Get) returns the
// FIRST. The fix: only trust the cache on the same Document generation; after a
// Rebind/Reset, fall back to a fresh first-occurrence search.

// TestPinDuplicateKeyAfterRebind learns a trajectory on one shape, then rebinds
// to a record whose duplicate key aligns with the learned position.
func TestPinDuplicateKeyAfterRebind(t *testing.T) {
	learnRec := []byte(`{"":0,"b":0}`)   // "b" is the 2nd member
	readRec := []byte(`{"b":0,"b":1}`)   // duplicate "b"; first occurrence wins

	d := Index(learnRec)
	p := d.Pin([]string{"b"})

	p.Rebind(readRec)
	v, _, ok := p.Get(0)
	if !ok {
		t.Fatalf("pin get after rebind: not found")
	}
	want, _, _, _ := Get(readRec, "b")
	if !bytes.Equal(v, want) {
		t.Fatalf("pin returned non-first duplicate: got %q want %q", v, want)
	}
	if string(v) != "0" {
		t.Fatalf("expected first occurrence \"0\", got %q", v)
	}
}

// TestPinSameDocumentCacheStillFast confirms the gen-guard does not break the
// same-document fast path: repeated reads of one unchanged document return the
// correct cached value.
func TestPinSameDocumentCache(t *testing.T) {
	d := Index([]byte(`{"a":1,"b":2,"c":3}`))
	p := d.Pin([]string{"b"})
	for i := 0; i < 5; i++ {
		v, vt, ok := p.Get(0)
		if !ok || vt != Number || string(v) != "2" {
			t.Fatalf("iter %d: got %q %v %v", i, v, vt, ok)
		}
	}
}

// TestTransposeDuplicateKey covers the multi-column Frame path: a batch where a
// later record has a duplicate of the column key must still yield the first
// occurrence in every cell.
func TestTransposeDuplicateKey(t *testing.T) {
	recs := [][]byte{
		[]byte(`{"":0,"b":0}`),
		[]byte(`{"b":0,"b":1}`),
		[]byte(`{"x":9,"b":7}`),
	}
	f := Transpose(recs, []string{"b"})
	for r, rec := range recs {
		want, _, _, err := Get(rec, "b")
		if err != nil {
			t.Fatalf("record %d: oracle Get failed", r)
		}
		if !bytes.Equal(f.Cols[0][r], want) {
			t.Fatalf("record %d: frame %q != first-occurrence %q", r, f.Cols[0][r], want)
		}
	}
}
