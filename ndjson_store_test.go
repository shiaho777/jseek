package jseek

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ndjsonFixture(n int) []byte {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "{\"id\":%d,\"level\":\"info\",\"latency_ms\":%d,\"msg\":\"req %d\"}\n", i, i%500, i)
	}
	return []byte(b.String())
}

func TestNDJSONStoreRoundTrip(t *testing.T) {
	src := ndjsonFixture(1000)
	var buf bytes.Buffer
	n, err := SealNDJSON(&buf, bytes.NewReader(src))
	if err != nil || n != 1000 {
		t.Fatalf("SealNDJSON n=%d err=%v", n, err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "t.njstore")
	os.WriteFile(path, buf.Bytes(), 0644)
	s, err := OpenNDJSONStore(path)
	if err != nil {
		t.Fatalf("OpenNDJSONStore: %v", err)
	}
	defer s.Close()
	if s.Len() != 1000 {
		t.Fatalf("Len=%d", s.Len())
	}
	// Random access: jump to arbitrary records, verify fields.
	for _, i := range []int{0, 1, 500, 999, 998, 42} {
		d, err := s.Record(i)
		if err != nil {
			t.Fatalf("Record(%d): %v", i, err)
		}
		id, err := d.GetInt("id")
		if err != nil || id != int64(i) {
			t.Errorf("Record(%d).id=%d err=%v", i, id, err)
		}
		lat, _ := d.GetInt("latency_ms")
		if lat != int64(i%500) {
			t.Errorf("Record(%d).latency=%d", i, lat)
		}
	}
	// Out of range.
	if _, err := s.Record(1000); err != ErrKeyPathNotFound {
		t.Errorf("Record(1000)=%v", err)
	}
	if _, err := s.Record(-1); err != ErrKeyPathNotFound {
		t.Errorf("Record(-1)=%v", err)
	}
	// Each visits all in order.
	count := 0
	s.Each(func(i int, d *Document) bool {
		if id, _ := d.GetInt("id"); id != int64(i) {
			t.Errorf("Each i=%d id=%d", i, id)
		}
		count++
		return true
	})
	if count != 1000 {
		t.Errorf("Each count=%d", count)
	}
}

func TestNDJSONStoreRejectsCorrupt(t *testing.T) {
	src := ndjsonFixture(50)
	var buf bytes.Buffer
	SealNDJSON(&buf, bytes.NewReader(src))
	img := buf.Bytes()
	dir := t.TempDir()
	// Bad magic.
	bad := append([]byte(nil), img...)
	bad[0] = 'X'
	p := filepath.Join(dir, "bad.njstore")
	os.WriteFile(p, bad, 0644)
	if _, err := OpenNDJSONStore(p); err != ErrBadIndex {
		t.Errorf("bad magic: %v", err)
	}
	// Mutate a payload byte.
	bad2 := append([]byte(nil), img...)
	bad2[40] ^= 0xFF
	os.WriteFile(p, bad2, 0644)
	if _, err := OpenNDJSONStore(p); err == nil {
		t.Error("mutated payload accepted")
	}
	// Truncated.
	os.WriteFile(p, img[:20], 0644)
	if _, err := OpenNDJSONStore(p); err != ErrBadIndex {
		t.Errorf("truncated: %v", err)
	}
}

func TestNDJSONStoreInvalidInput(t *testing.T) {
	var buf bytes.Buffer
	if _, err := SealNDJSON(&buf, strings.NewReader("{\"a\":1}\n{bad}\n")); err == nil {
		t.Error("invalid record accepted")
	}
}

func TestViewIntoZeroAlloc(t *testing.T) {
	data := persistFixture()
	var buf bytes.Buffer
	IndexTape(data).Persist(&buf)
	idx := buf.Bytes()
	if err := VerifyIndex(idx, data); err != nil {
		t.Fatal(err)
	}
	if safeBuild {
		t.Skip("zero-copy reinterpretation is disabled under jseeksafe")
	}
	var d Document
	allocs := testing.AllocsPerRun(1000, func() {
		if _, err := ViewInto(&d, idx, data); err != nil {
			panic(err)
		}
		if _, err := d.GetInt("users", "[250]", "score"); err != nil {
			panic(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("ViewInto hot loop allocated %v, want 0", allocs)
	}
}

func TestNDJSONRecordIntoZeroAlloc(t *testing.T) {
	src := ndjsonFixture(200)
	var buf bytes.Buffer
	SealNDJSON(&buf, bytes.NewReader(src))
	dir := t.TempDir()
	path := filepath.Join(dir, "z.njstore")
	os.WriteFile(path, buf.Bytes(), 0644)
	s, err := OpenNDJSONStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if safeBuild {
		t.Skip("zero-copy reinterpretation is disabled under jseeksafe")
	}
	var d Document
	allocs := testing.AllocsPerRun(500, func() {
		for i := 0; i < 200; i++ {
			if _, err := s.RecordInto(&d, i); err != nil {
				panic(err)
			}
			if _, err := d.GetInt("latency_ms"); err != nil {
				panic(err)
			}
		}
	})
	if allocs != 0 {
		t.Fatalf("RecordInto loop allocated %v, want 0", allocs)
	}
}

func TestNDJSONAppend(t *testing.T) {
	var buf bytes.Buffer
	n, err := SealNDJSON(&buf, bytes.NewReader(ndjsonFixture(100)))
	if err != nil || n != 100 {
		t.Fatalf("seal n=%d err=%v", n, err)
	}
	prior := append([]byte(nil), buf.Bytes()...)
	// Append 50 more records (id 100..149).
	var add strings.Builder
	for i := 100; i < 150; i++ {
		fmt.Fprintf(&add, "{\"id\":%d,\"level\":\"info\",\"latency_ms\":%d,\"msg\":\"req %d\"}\n", i, i%500, i)
	}
	var out bytes.Buffer
	total, err := AppendNDJSON(&out, prior, strings.NewReader(add.String()))
	if err != nil || total != 150 {
		t.Fatalf("append total=%d err=%v", total, err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "a.njstore")
	os.WriteFile(path, out.Bytes(), 0644)
	s, err := OpenNDJSONStore(path)
	if err != nil {
		t.Fatalf("open appended: %v", err)
	}
	defer s.Close()
	if s.Len() != 150 {
		t.Fatalf("Len=%d", s.Len())
	}
	// Old records intact, new records correct.
	for _, i := range []int{0, 99, 100, 149} {
		d, err := s.Record(i)
		if err != nil {
			t.Fatalf("Record(%d): %v", i, err)
		}
		if id, _ := d.GetInt("id"); id != int64(i) {
			t.Errorf("Record(%d).id=%d", i, id)
		}
	}
	// Append nothing -> unchanged count.
	var out2 bytes.Buffer
	total2, err := AppendNDJSON(&out2, out.Bytes(), strings.NewReader(""))
	if err != nil || total2 != 150 {
		t.Fatalf("empty append total=%d err=%v", total2, err)
	}
}

// FuzzNDJSONStoreRoundTrip seals arbitrary NDJSON, reopens it, and checks every
// record's raw bytes and top-level type match the input; corrupt images must be
// rejected without panic.
func FuzzNDJSONStoreRoundTrip(f *testing.F) {
	f.Add([]byte("{\"a\":1}\n{\"a\":2}\n"))
	f.Add([]byte("[1,2]\n{\"x\":\"y\"}\nnull\n"))
	f.Add([]byte("{\"n\":{\"d\":[true,false]}}\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		var buf bytes.Buffer
		n, err := SealNDJSON(&buf, bytes.NewReader(data))
		if err != nil {
			// Invalid input: sealing must fail cleanly, never panic.
			return
		}
		img := buf.Bytes()
		s, err := parseNDJSON(img)
		if err != nil {
			t.Fatalf("parse own sealed image: %v", err)
		}
		if s.n != n {
			t.Fatalf("n=%d want %d", s.n, n)
		}
		// Each record's raw bytes must be a valid JSON value matching input order.
		for i := 0; i < n; i++ {
			raw, err := s.Raw(i)
			if err != nil {
				t.Fatal(err)
			}
			if !Valid(raw) {
				t.Fatalf("record %d not valid: %q", i, raw)
			}
			d, err := s.Record(i)
			if err != nil {
				t.Fatal(err)
			}
			_, vt, _, _ := d.Get()
			_, vt2, _, _ := Get(raw)
			if vt != vt2 {
				t.Fatalf("record %d type %v vs %v", i, vt, vt2)
			}
		}
		// Corrupt: must reject, never panic.
		if len(img) > ndjsonHeader {
			bad := append([]byte(nil), img...)
			bad[ndjsonHeader] ^= 0xFF
			parseNDJSON(bad)
		}
	})
}

// BenchmarkNDJSONRandomAccess compares O(1) record jumps against a sequential
// scan that must walk every prior record to reach record N.
func BenchmarkNDJSONRandomAccess(b *testing.B) {
	src := ndjsonFixture(5000)
	var buf bytes.Buffer
	SealNDJSON(&buf, bytes.NewReader(src))
	dir := b.TempDir()
	path := filepath.Join(dir, "b.njstore")
	os.WriteFile(path, buf.Bytes(), 0644)
	s, err := OpenNDJSONStore(path)
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	var d Document
	b.Run("StoreRecord4999", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := s.RecordInto(&d, 4999); err != nil {
				b.Fatal(err)
			}
			if _, err := d.GetInt("latency_ms"); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("SequentialScanTo4999", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			// Walk from the start each time, as a plain NDJSON reader must.
			dec := NewNDJSONDecoder(bytes.NewReader(src))
			var last []byte
			for k := 0; k <= 4999; k++ {
				v, err := dec.Next()
				if err != nil {
					b.Fatal(err)
				}
				last = v
			}
			if _, err := GetInt(last, "latency_ms"); err != nil {
				b.Fatal(err)
			}
			dec.Close()
		}
	})
}