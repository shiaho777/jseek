package jseek

import (
	"encoding/json"
	"testing"
)

// FuzzPinMatchesGet is the trust line for the pinned-trajectory cache: after
// pinning against one document and rebinding to ANOTHER (possibly differently
// shaped) document, every Pinned.Get must return EXACTLY what a fresh stateless
// Get returns. The cache may only affect speed, never correctness.
func FuzzPinMatchesGet(f *testing.F) {
	seeds := []struct{ a, b string }{
		{`{"a":1,"b":2}`, `{"a":10,"b":20}`},
		{`{"a":1,"b":2}`, `{"b":2,"a":1}`},
		{`{"a":1,"b":2}`, `{"x":9,"a":1,"b":2}`},
		{`{"x":{"y":1}}`, `{"x":{"y":2}}`},
		{`{"a":1}`, `{"c":3}`},
		{`{"a":"s","b":true}`, `{"a":"t","b":false}`},
	}
	for _, s := range seeds {
		f.Add([]byte(s.a), []byte(s.b))
	}

	paths := [][]string{
		{"a"}, {"b"}, {"x", "y"}, {"a", "b"}, {"missing"},
	}

	f.Fuzz(func(t *testing.T, a, b []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic: pin %q rebind %q: %v", a, b, r)
			}
		}()
		if !json.Valid(a) || !json.Valid(b) {
			return
		}

		da := Index(a)
		p := da.Pin(paths...)
		// Use it once on a so the cache is warm.
		for i := range paths {
			p.Get(i)
		}

		// Rebind to b and compare every path against stateless Get on b.
		p.Rebind(b)
		for i, path := range paths {
			gv, gvt, _, gerr := Get(b, path...)
			pv, pvt, pok := p.Get(i)

			if (gerr == nil) != pok {
				t.Fatalf("path %v: presence mismatch on b=%q Get.ok=%v pin.ok=%v", path, b, gerr == nil, pok)
			}
			if gerr != nil {
				continue
			}
			if pvt != gvt || string(pv) != string(gv) {
				t.Fatalf("path %v on b=%q: pin=(%v,%q) Get=(%v,%q)", path, b, pvt, pv, gvt, gv)
			}
		}
	})
}
