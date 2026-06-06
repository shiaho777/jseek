package jseek

import (
	"strconv"
	"strings"
	"testing"
)

// The indexed engine must remain correct for documents larger than the
// indexable ceiling: it builds no structural index and transparently routes
// every query to the unlimited stateless scanner. We exercise this by lowering
// maxIndexable for the duration of the test (production keeps the 512 MiB
// ceiling) so we need not allocate half a gigabyte.

func withSmallCeiling(t *testing.T, ceiling int, fn func()) {
	t.Helper()
	saved := maxIndexable
	maxIndexable = ceiling
	defer func() { maxIndexable = saved }()
	fn()
}

// buildWideDoc returns a valid JSON object whose length exceeds n bytes, with
// known fields at the front, middle, and a deep array element.
func buildWideDoc(n int) []byte {
	var b strings.Builder
	b.WriteString(`{"head":"H","nums":[`)
	i := 0
	for b.Len() < n {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"i":`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`,"tag":"t`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`"}`)
		i++
	}
	b.WriteString(`],"tail":"T"}`)
	return []byte(b.String())
}

func TestOversizeDocumentMatchesStateless(t *testing.T) {
	data := buildWideDoc(4096)
	withSmallCeiling(t, 1024, func() {
		if len(data) <= maxIndexable {
			t.Fatalf("test doc (%d) not above ceiling (%d)", len(data), maxIndexable)
		}
		d := Index(data)
		if !d.oversize {
			t.Fatal("expected document to be flagged oversize")
		}
		// A spread of paths: shallow, deep array element, tail.
		paths := [][]string{
			{"head"},
			{"tail"},
			{"nums", "[0]", "tag"},
			{"nums", "[5]", "i"},
			{"nums", "[10]", "tag"},
		}
		for _, p := range paths {
			gotV, gotT, _, gotErr := d.Get(p...)
			wantV, wantT, _, wantErr := Get(data, p...)
			if (gotErr == nil) != (wantErr == nil) || gotT != wantT || string(gotV) != string(wantV) {
				t.Errorf("oversize Get(%v)=(%q,%v,%v) want (%q,%v,%v)",
					p, gotV, gotT, gotErr, wantV, wantT, wantErr)
			}
			if d.Exists(p...) != Exists(data, p...) {
				t.Errorf("oversize Exists(%v) mismatch", p)
			}
		}
	})
}

func TestOversizeTapeAndEachDocAndPin(t *testing.T) {
	data := buildWideDoc(4096)
	withSmallCeiling(t, 1024, func() {
		// IndexTape on an oversized doc must not build a tape and must still
		// return correct values.
		d := IndexTape(data)
		if d.hasTape {
			t.Fatal("oversize document should not have a tape")
		}
		v, err := d.GetString("nums", "[3]", "tag")
		if err != nil || v != "t3" {
			t.Fatalf("oversize tape GetString = %q,%v want t3", v, err)
		}

		// EachDoc must agree with the stateless multi-path matcher.
		paths := [][]string{{"head"}, {"nums", "[7]", "i"}, {"tail"}}
		q := CompileStrings(paths...)
		got := map[int]string{}
		q.EachDoc(d, func(idx int, value []byte, vt ValueType, err error) {
			got[idx] = string(value)
		})
		want := map[int]string{}
		q.Each(data, func(idx int, value []byte, vt ValueType, err error) {
			want[idx] = string(value)
		})
		if len(got) != len(want) {
			t.Fatalf("EachDoc found %d paths, Each found %d", len(got), len(want))
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("EachDoc path %d = %q want %q", k, got[k], v)
			}
		}

		// Pin must fall back and still return correct values.
		pn := d.Pin([]string{"head"}, []string{"tail"})
		if hv, _, ok := pn.Get(0); !ok || string(hv) != "H" {
			t.Errorf("oversize Pin head = %q,%v want H", hv, ok)
		}
		if tv, _, ok := pn.Get(1); !ok || string(tv) != "T" {
			t.Errorf("oversize Pin tail = %q,%v want T", tv, ok)
		}
	})
}
