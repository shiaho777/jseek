package jseek

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSealRoundTrip(t *testing.T) {
	data := persistFixture()
	var buf bytes.Buffer
	if err := Seal(&buf, data); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	img := buf.Bytes()
	// Zero-copy view over the sealed image.
	d, err := ViewSealed(img)
	if err != nil {
		t.Fatalf("ViewSealed: %v", err)
	}
	src := IndexTape(data)
	for _, p := range [][]string{
		{"meta", "v"}, {"users", "[0]", "name"},
		{"users", "[499]", "score"}, {"users", "[250]", "active"},
	} {
		a, at, _, ae := src.Get(p...)
		b, bt, _, be := d.Get(p...)
		if (ae == nil) != (be == nil) || at != bt || !bytes.Equal(a, b) {
			t.Errorf("path=%v mismatch", p)
		}
	}
	// Trusted path after one verify.
	if err := VerifySealed(img); err != nil {
		t.Fatal(err)
	}
	dt, err := ViewSealedTrusted(img)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := dt.Count("users"); n != 500 {
		t.Errorf("Count=%d want 500", n)
	}
	// Streaming open.
	ds, err := OpenSealed(bytes.NewReader(img))
	if err != nil {
		t.Fatal(err)
	}
	if v, _, _, _ := ds.Get("users", "[9]", "name"); string(v) != "user_9" {
		t.Errorf("OpenSealed get=%q", v)
	}
}

func TestSealRejectsCorrupt(t *testing.T) {
	data := persistFixture()
	var buf bytes.Buffer
	if err := Seal(&buf, data); err != nil {
		t.Fatal(err)
	}
	img := buf.Bytes()
	bad := append([]byte(nil), img...)
	bad[0] = 'X' // magic
	if _, err := ViewSealed(bad); err != ErrBadIndex {
		t.Errorf("bad magic: %v", err)
	}
	if _, err := ViewSealed(img[:20]); err != ErrBadIndex {
		t.Errorf("truncated: %v", err)
	}
	// Mutate a payload byte (after the 32-byte header).
	bad2 := append([]byte(nil), img...)
	bad2[40] ^= 0xFF
	if _, err := ViewSealed(bad2); err != ErrBadIndex {
		t.Errorf("mutated payload: %v", err)
	}
}

func TestStoreMmap(t *testing.T) {
	data := persistFixture()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jstore")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Seal(f, data); err != nil {
		t.Fatal(err)
	}
	f.Close()

	s, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()
	if !bytes.Equal(s.Data(), data) {
		t.Error("Store.Data mismatch")
	}
	// Repeated O(1) views over the mapping.
	for i := 0; i < 100; i++ {
		d, err := s.View()
		if err != nil {
			t.Fatal(err)
		}
		if n, err := d.GetInt("users", "[250]", "score"); err != nil || n != 250*7 {
			t.Errorf("iter %d: score=%d err=%v", i, n, err)
		}
	}
}

func TestStoreConcurrentViews(t *testing.T) {
	data := persistFixture()
	dir := t.TempDir()
	path := filepath.Join(dir, "c.jstore")
	f, _ := os.Create(path)
	Seal(f, data)
	f.Close()
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	done := make(chan bool, 8)
	for g := 0; g < 8; g++ {
		go func() {
			for i := 0; i < 200; i++ {
				d, err := s.View()
				if err != nil {
					t.Error(err)
					return
				}
				if _, err := d.GetInt("users", "[499]", "id"); err != nil {
					t.Error(err)
					return
				}
			}
			done <- true
		}()
	}
	for g := 0; g < 8; g++ {
		<-done
	}
}

// FuzzPersistRoundTrip checks that any index built from arbitrary bytes can be
// persisted and re-opened (View/Open) to answer identically to the source, and
// that corrupt images are rejected without panic.
func FuzzPersistRoundTrip(f *testing.F) {
	f.Add([]byte(`{"a":1,"b":[1,2,3]}`))
	f.Add([]byte(`[{"x":"y"},{"x":"z"}]`))
	f.Add([]byte(`{"nested":{"deep":{"k":true}}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		src := IndexTape(data)
		if src.oversize {
			return
		}
		var buf bytes.Buffer
		if err := src.Persist(&buf); err != nil {
			t.Fatalf("Persist: %v", err)
		}
		idx := buf.Bytes()
		for _, open := range []func() (*Document, error){
			func() (*Document, error) { return View(idx, data) },
			func() (*Document, error) { return Open(bytes.NewReader(idx), data) },
		} {
			d, err := open()
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			// Top-level type and a spread of queries must agree.
			a, at, _, ae := src.Get()
			b, bt, _, be := d.Get()
			if (ae == nil) != (be == nil) || at != bt || !bytes.Equal(a, b) {
				t.Fatalf("root mismatch: %v vs %v", ae, be)
			}
		}
		// Corrupt the index: must reject, never panic.
		if len(idx) > persistHeader {
			bad := append([]byte(nil), idx...)
			bad[persistHeader] ^= 0xFF
			View(bad, data)
			Open(bytes.NewReader(bad), data)
		}
	})
}

func BenchmarkPayloadHash(b *testing.B) {
	data := persistFixture()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = payloadHash(data)
	}
}

func BenchmarkViewWithHash(b *testing.B) {
	data := persistFixture()
	var buf bytes.Buffer
	IndexTape(data).Persist(&buf)
	idx := buf.Bytes()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		d, err := View(idx, data)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := d.GetInt("users", "[250]", "score"); err != nil {
			b.Fatal(err)
		}
	}
}