package jseek

import (
	"encoding/json"
	"testing"
)

func TestValidStrict(t *testing.T) {
	// Accepted by both Valid and ValidStrict.
	good := []string{
		`{}`, `[]`, `null`, `true`, `42`, `"hi"`,
		`{"a":1,"b":[1,2,{"c":"é"}]}`,
		`{"A":"paired surrogate"}`,
		`{"a":1,"b":2}`,
	}
	for _, s := range good {
		if !Valid([]byte(s)) || !ValidStrict([]byte(s)) {
			t.Errorf("both should accept %q", s)
		}
	}
	// Accepted by Valid (syntax) but rejected by ValidStrict (safety).
	strictOnly := []string{
		`{"a":1,"a":2}`,        // duplicate key
		`{"x":"\uD800"}`,       // lone high surrogate
		`{"x":"\uDC00"}`,       // lone low surrogate
		"\"\xff\"",             // invalid UTF-8
		"\"\xed\xa0\x80\"",     // UTF-8-encoded surrogate
		`{"k":"\uD800A"}`, // high surrogate not followed by low
	}
	for _, s := range strictOnly {
		if !Valid([]byte(s)) {
			t.Errorf("Valid should accept (syntax) %q", s)
		}
		if ValidStrict([]byte(s)) {
			t.Errorf("ValidStrict should reject %q", s)
		}
	}
	// Rejected by both (syntax errors).
	bad := []string{``, `{`, `[1,]`, `01`, `{"a":}`, `tru`}
	for _, s := range bad {
		if Valid([]byte(s)) || ValidStrict([]byte(s)) {
			t.Errorf("both should reject %q", s)
		}
	}
}

// TestValidStrictSubsetOfStdlib checks the soundness invariant: anything
// ValidStrict accepts must also decode cleanly with the stdlib (v1 replaces
// invalid UTF-8/surrogates rather than rejecting them, so ValidStrict is a
// strict subset of stdlib acceptance — it implements the stricter json/v2
// safety defaults). The reverse need not hold.
func TestValidStrictSubsetOfStdlib(t *testing.T) {
	cases := []string{
		`{"a":"é"}`, `{"a":"😀"}`, `{"a":"A"}`,
		"\"\xff\"", "\"\xc0\xaf\"", `"\uD800"`, `"\uDC00"`,
		`{"a":"\uD800A"}`, `[1,2,3]`, `{"a":null}`, `{"a":1,"a":2}`,
		`{"nested":{"k":[true,false,null,1.5e3]}}`,
	}
	for _, s := range cases {
		if ValidStrict([]byte(s)) {
			var v any
			if err := json.Unmarshal([]byte(s), &v); err != nil {
				t.Errorf("ValidStrict accepted %q but stdlib decode failed: %v", s, err)
			}
		}
	}
}

func TestValidStrictZeroAllocOnScalar(t *testing.T) {
	// Scalars/arrays without object keys must not allocate even in strict mode.
	data := []byte(`[1,2,"é",true,null,{"a":1}]`)
	// The nested object allocates a key map; check a pure scalar array.
	scalar := []byte(`[1,2,3,"hello",true]`)
	allocs := testing.AllocsPerRun(500, func() {
		if !ValidStrict(scalar) {
			panic("fixture must be valid")
		}
	})
	if allocs != 0 {
		t.Fatalf("ValidStrict on scalar array allocated %v, want 0", allocs)
	}
	_ = data
}