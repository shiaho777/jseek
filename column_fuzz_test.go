package jseek

import (
	"bytes"
	"encoding/json"
	"testing"
)

// FuzzTransposeMatchesGet is the trust line for columnar transposition: for an
// arbitrary batch of records (mixed shapes allowed), every cell of every column
// must equal what a fresh stateless Get returns for that record and path. The
// learned-trajectory cache may only affect speed, never correctness.
func FuzzTransposeMatchesGet(f *testing.F) {
	// Seeds: each input encodes up to 4 records separated by '\n'.
	seeds := []string{
		`{"a":1,"b":2}` + "\n" + `{"a":3,"b":4}`,
		`{"a":1}` + "\n" + `{"b":2,"a":3}` + "\n" + `{"a":5}`,
		`{"x":{"y":1}}` + "\n" + `{"x":{"y":2}}`,
		`{"a":"s"}` + "\n" + `{"a":"t","b":true}`,
		`{"a":1,"b":2}` + "\n" + `{"b":20,"a":10}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	paths := [][]string{{"a"}, {"b"}, {"x", "y"}}

	f.Fuzz(func(t *testing.T, blob []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on %q: %v", blob, r)
			}
		}()

		// Split into records; keep only valid JSON ones.
		var records [][]byte
		for _, line := range bytes.Split(blob, []byte{'\n'}) {
			ln := bytes.TrimSpace(line)
			if len(ln) == 0 || !json.Valid(ln) {
				continue
			}
			records = append(records, ln)
		}
		if len(records) == 0 {
			return
		}

		f := Transpose(records, paths...)
		if f.Rows != len(records) {
			t.Fatalf("rows %d != records %d", f.Rows, len(records))
		}

		for c, path := range paths {
			for r, rec := range records {
				gv, gvt, _, gerr := Get(rec, path...)
				present := f.Types[c][r] != NotExist

				if (gerr == nil) != present {
					t.Fatalf("col %v row %d: presence mismatch on %q Get.ok=%v frame.present=%v",
						path, r, rec, gerr == nil, present)
				}
				if gerr != nil {
					continue
				}
				if f.Types[c][r] != gvt {
					t.Fatalf("col %v row %d on %q: type frame=%v Get=%v", path, r, rec, f.Types[c][r], gvt)
				}
				if !bytes.Equal(f.Cols[c][r], gv) {
					t.Fatalf("col %v row %d on %q: value frame=%q Get=%q", path, r, rec, f.Cols[c][r], gv)
				}
			}
		}
	})
}
