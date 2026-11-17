package jseek

import (
	"bytes"
	"strings"
	"testing"
)

func persistFixture() []byte {
	var b strings.Builder
	b.WriteString(`{"meta":{"v":"1.0"},"users":[`)
	for i := 0; i < 500; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"id":`)
		b.WriteString(itoa(i))
		b.WriteString(`,"name":"user_`)
		b.WriteString(itoa(i))
		b.WriteString(`","active":true,"score":`)
		b.WriteString(itoa(i * 7))
		b.WriteString(`}`)
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// TestPersistRoundTrip proves a persisted index reconstructs a Document that
// answers every query identically to a freshly built one — with no re-scan.
func TestPersistRoundTrip(t *testing.T) {
	data := persistFixture()
	for _, withTape := range []bool{false, true} {
		var src *Document
		if withTape {
			src = IndexTape(data)
		} else {
			src = Index(data)
		}
		var buf bytes.Buffer
		if err := src.Persist(&buf); err != nil {
			t.Fatalf("Persist: %v", err)
		}
		opened, err := Open(&buf, data)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		// Query agreement across a spread of paths.
		paths := [][]string{
			{"meta", "v"},
			{"users", "[0]", "name"},
			{"users", "[250]", "score"},
			{"users", "[499]", "active"},
			{"users", "[499]", "name"},
		}
		for _, p := range paths {
			a, at, _, ae := src.Get(p...)
			b, bt, _, be := opened.Get(p...)
			if (ae == nil) != (be == nil) || at != bt || !bytes.Equal(a, b) {
				t.Errorf("tape=%v path=%v: src=%q,%v,%v opened=%q,%v,%v",
					withTape, p, a, at, ae, b, bt, be)
			}
		}
		// Count and typed getters agree too.
		if n1, _ := src.Count("users"); true {
			n2, _ := opened.Count("users")
			if n1 != n2 || n1 != 500 {
				t.Errorf("Count mismatch: %d vs %d", n1, n2)
			}
		}
	}
}

// TestPersistRejectsMismatch proves the hash binding catches stale indexes.
func TestPersistRejectsMismatch(t *testing.T) {
	data := persistFixture()
	var buf bytes.Buffer
	if err := Index(data).Persist(&buf); err != nil {
		t.Fatal(err)
	}
	// Mutate one byte of the payload -> hash mismatch.
	bad := append([]byte(nil), data...)
	bad[10] ^= 0xFF
	if _, err := Open(&buf, bad); err != ErrBadIndex {
		t.Errorf("expected ErrBadIndex on mutated payload, got %v", err)
	}
	// Truncated index.
	if _, err := Open(bytes.NewReader(buf.Bytes()[:20]), data); err != ErrBadIndex {
		t.Errorf("expected ErrBadIndex on truncated index, got %v", err)
	}
	// Wrong magic.
	wrong := append([]byte(nil), buf.Bytes()...)
	wrong[0] = 'X'
	if _, err := Open(bytes.NewReader(wrong), data); err != ErrBadIndex {
		t.Errorf("expected ErrBadIndex on bad magic, got %v", err)
	}
}

// TestPersistSize reports the index overhead vs raw payload.
func TestPersistSize(t *testing.T) {
	data := persistFixture()
	d := IndexTape(data)
	t.Logf("payload=%d bytes, index(with tape)=%d bytes (%.1f%%)",
		len(data), d.IndexSize(), 100*float64(d.IndexSize())/float64(len(data)))
}

// BenchmarkOpenVsIndex is the breakthrough measurement: opening a persisted
// index (no Stage-1 scan) vs rebuilding it from raw bytes.
func BenchmarkOpenVsIndex(b *testing.B) {
	data := persistFixture()
	var buf bytes.Buffer
	if err := IndexTape(data).Persist(&buf); err != nil {
		b.Fatal(err)
	}
	idxBytes := buf.Bytes()

	b.Run("RebuildIndexTape", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			d := IndexTape(data)
			if _, err := d.GetInt("users", "[250]", "score"); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("OpenPersisted", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			d, err := Open(bytes.NewReader(idxBytes), data)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := d.GetInt("users", "[250]", "score"); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("ViewZeroCopy", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			d, err := View(idxBytes, data)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := d.GetInt("users", "[250]", "score"); err != nil {
				b.Fatal(err)
			}
		}
	})
	// Canonical mmap pattern: verify the mapping ONCE, then open O(1) many times.
	if err := VerifyIndex(idxBytes, data); err != nil {
		b.Fatal(err)
	}
	b.Run("ViewTrustedAfterVerify", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			d, err := ViewTrusted(idxBytes, data)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := d.GetInt("users", "[250]", "score"); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// TestViewZeroCopy proves View reconstructs an identical Document with no
// copying, and that it rejects corrupt/mismatched input.
func TestViewZeroCopy(t *testing.T) {
	data := persistFixture()
	for _, withTape := range []bool{false, true} {
		var src *Document
		if withTape {
			src = IndexTape(data)
		} else {
			src = Index(data)
		}
		var buf bytes.Buffer
		if err := src.Persist(&buf); err != nil {
			t.Fatal(err)
		}
		idx := buf.Bytes()
		v, err := View(idx, data)
		if err != nil {
			t.Fatalf("View: %v", err)
		}
		for _, p := range [][]string{
			{"meta", "v"}, {"users", "[0]", "name"},
			{"users", "[499]", "score"}, {"users", "[250]", "active"},
		} {
			a, at, _, ae := src.Get(p...)
			b, bt, _, be := v.Get(p...)
			if (ae == nil) != (be == nil) || at != bt || !bytes.Equal(a, b) {
				t.Errorf("tape=%v path=%v mismatch", withTape, p)
			}
		}
		// Corrupt: bad magic, truncated, mutated payload.
		bad := append([]byte(nil), idx...)
		bad[0] = 'X'
		if _, err := View(bad, data); err != ErrBadIndex {
			t.Errorf("bad magic: %v", err)
		}
		if _, err := View(idx[:20], data); err != ErrBadIndex {
			t.Errorf("truncated: %v", err)
		}
		badData := append([]byte(nil), data...)
		badData[5] ^= 0xFF
		if _, err := View(idx, badData); err != ErrBadIndex {
			t.Errorf("mutated payload: %v", err)
		}
	}
}

// TestViewZeroAlloc confirms the zero-copy path allocates nothing on open.
// In the default (unsafe) build the index arrays are reinterpreted, so only
// the Document struct allocates (1). In the jseeksafe build u32Slice decodes
// into fresh arrays (structural + tape = 2 extra), so up to 3 are expected —
// the guarantee there is memory safety, not zero allocation.
func TestViewZeroAlloc(t *testing.T) {
	data := persistFixture()
	var buf bytes.Buffer
	if err := IndexTape(data).Persist(&buf); err != nil {
		t.Fatal(err)
	}
	idx := buf.Bytes()
	allocs := testing.AllocsPerRun(1000, func() {
		d, err := ViewTrusted(idx, data)
		if err != nil {
			panic(err)
		}
		if _, err := d.GetInt("users", "[250]", "score"); err != nil {
			panic(err)
		}
	})
	want := 1.0
	if safeBuild {
		want = 3.0
	}
	if allocs > want {
		t.Fatalf("View path allocated %v times/run, want <=%v", allocs, want)
	}
}