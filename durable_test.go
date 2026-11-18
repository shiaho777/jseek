package jseek

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
)

// sealTemp writes a sealed base file and returns its path.
func sealTemp(t *testing.T, data string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "base.jstore")
	if err := SealFileAtomic(p, []byte(data), 0644); err != nil {
		t.Fatalf("seal: %v", err)
	}
	return p
}

func TestWALDurabilityRoundTrip(t *testing.T) {
	base := sealTemp(t, `{"a":1,"b":2}`)
	walPath := base + ".wal"
	w, err := OpenWAL(base, walPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Set([]byte(`99`), "a"); err != nil {
		t.Fatal(err)
	}
	if err := w.Set([]byte(`"hi"`), "c"); err != nil {
		t.Fatal(err)
	}
	w.Close()

	// Reopen: edits must survive via WAL replay.
	w2, err := OpenWAL(base, walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	if w2.Pending() != 2 {
		t.Fatalf("pending=%d want 2", w2.Pending())
	}
	v, err := w2.GetInt("a")
	if err != nil || v != 99 {
		t.Errorf("a=%d err=%v want 99", v, err)
	}
}

// TestWALTornTailRecovery simulates a power loss mid-append: the last record is
// truncated. Replay must discard it and keep all prior committed edits.
func TestWALTornTailRecovery(t *testing.T) {
	base := sealTemp(t, `{"a":1}`)
	walPath := base + ".wal"
	w, err := OpenWAL(base, walPath)
	if err != nil {
		t.Fatal(err)
	}
	w.Set([]byte(`10`), "a")
	w.Set([]byte(`20`), "b")
	w.Close()

	// Append a torn record: valid header claiming a large value, but only a few
	// bytes present (simulates a crash during the write).
	f, err := os.OpenFile(walPath, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatal(err)
	}
	var hdr [8]byte
	hdr[0] = 'E'
	hdr[1] = byte(walSet)
	binary.LittleEndian.PutUint16(hdr[2:4], 1)
	binary.LittleEndian.PutUint32(hdr[4:8], 1000) // claims 1000 value bytes
	f.Write(hdr[:])
	f.Write([]byte("x")) // path
	f.Write([]byte("partial"))
	f.Close()

	w2, err := OpenWAL(base, walPath)
	if err != nil {
		t.Fatalf("reopen after torn tail: %v", err)
	}
	defer w2.Close()
	if w2.Pending() != 2 {
		t.Fatalf("pending=%d want 2 (torn record discarded)", w2.Pending())
	}
	if v, _ := w2.GetInt("b"); v != 20 {
		t.Errorf("b=%d want 20", v)
	}
	// The torn tail must have been truncated: a new write appends cleanly.
	if err := w2.Set([]byte(`30`), "c"); err != nil {
		t.Fatal(err)
	}
	w2.Close()
	w3, err := OpenWAL(base, walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer w3.Close()
	if w3.Pending() != 3 {
		t.Fatalf("pending=%d want 3", w3.Pending())
	}
}

// TestWALCorruptRecordCRC flips a byte in a committed record's value; the CRC
// must catch it and stop replay there (no silent acceptance of corrupt data).
func TestWALCorruptRecordCRC(t *testing.T) {
	base := sealTemp(t, `{"a":1}`)
	walPath := base + ".wal"
	w, err := OpenWAL(base, walPath)
	if err != nil {
		t.Fatal(err)
	}
	w.Set([]byte(`10`), "a")
	w.Set([]byte(`20`), "b")
	w.Close()

	data, err := os.ReadFile(walPath)
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt a byte in the second record's value region. Find record boundaries.
	// Record 1 length:
	pl1 := int(binary.LittleEndian.Uint16(data[2:4]))
	vl1 := int(binary.LittleEndian.Uint32(data[4:8]))
	rec1End := 8 + pl1 + vl1 + walCRCSize
	// Second record value starts at rec1End+8+pl2.
	pl2 := int(binary.LittleEndian.Uint16(data[rec1End+2 : rec1End+4]))
	valStart := rec1End + 8 + pl2
	data[valStart] ^= 0xFF // flip a value byte
	os.WriteFile(walPath, data, 0644)

	w2, err := OpenWAL(base, walPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer w2.Close()
	// First record is intact; second is corrupt and must be dropped.
	if w2.Pending() != 1 {
		t.Fatalf("pending=%d want 1 (corrupt record rejected)", w2.Pending())
	}
	if v, _ := w2.GetInt("a"); v != 10 {
		t.Errorf("a=%d want 10", v)
	}
}

// TestWALCompactCrashSafety verifies that after Compact the base file alone
// (WAL empty) holds all edits, and that a simulated crash between base-write and
// WAL-truncate still recovers correctly via idempotent replay.
func TestWALCompactCrashSafety(t *testing.T) {
	base := sealTemp(t, `{"a":1,"b":2}`)
	walPath := base + ".wal"
	w, err := OpenWAL(base, walPath)
	if err != nil {
		t.Fatal(err)
	}
	w.Set([]byte(`100`), "a")
	w.Set([]byte(`200`), "b")
	if err := w.Compact(); err != nil {
		t.Fatal(err)
	}
	w.Close()

	// After compact: WAL empty, base holds the edits.
	st, _ := os.Stat(walPath)
	if st.Size() != 0 {
		t.Fatalf("wal size=%d want 0 after compact", st.Size())
	}
	w2, err := OpenWAL(base, walPath)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := w2.GetInt("a"); v != 100 {
		t.Errorf("a=%d want 100", v)
	}
	if v, _ := w2.GetInt("b"); v != 200 {
		t.Errorf("b=%d want 200", v)
	}
	w2.Close()
}

// TestWALCompactIdempotentReplay simulates the crash window where the new base
// is durable but the WAL was not yet truncated: replaying the WAL over the new
// base must yield the same values (idempotent), not double-apply or corrupt.
func TestWALCompactIdempotentReplay(t *testing.T) {
	base := sealTemp(t, `{"a":1}`)
	walPath := base + ".wal"
	w, err := OpenWAL(base, walPath)
	if err != nil {
		t.Fatal(err)
	}
	w.Set([]byte(`42`), "a")
	// Capture the WAL bytes (the pending edit) before compact.
	walBytes, _ := os.ReadFile(walPath)
	if err := w.Compact(); err != nil {
		t.Fatal(err)
	}
	w.Close()
	// Simulate crash-before-truncate: restore the WAL over the compacted base.
	os.WriteFile(walPath, walBytes, 0644)

	w2, err := OpenWAL(base, walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	// Base already has a=42; replaying the edit re-applies 42 → still 42.
	if v, _ := w2.GetInt("a"); v != 42 {
		t.Errorf("a=%d want 42 (idempotent)", v)
	}
	// Compacting again folds the redundant edit cleanly.
	if err := w2.Compact(); err != nil {
		t.Fatal(err)
	}
	if v, _ := w2.GetInt("a"); v != 42 {
		t.Errorf("a=%d after recompact want 42", v)
	}
}

func TestWriteFileAtomicNoTorn(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	// Write initial.
	if err := WriteFileAtomic(p, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	// Overwrite.
	if err := WriteFileAtomic(p, []byte("replacement-longer"), 0644); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "replacement-longer" {
		t.Errorf("got %q", got)
	}
	// No temp files left behind.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "f" {
			t.Errorf("leftover temp: %s", e.Name())
		}
	}
}

func TestWALCRCFormat(t *testing.T) {
	e := walEdit{path: "a", op: walSet, val: []byte(`123`)}
	rec := walRecordBytes(e)
	// Verify the embedded CRC matches.
	n := len(rec)
	want := binary.LittleEndian.Uint32(rec[n-4:])
	got := crc32.ChecksumIEEE(rec[1 : n-4])
	if want != got {
		t.Errorf("crc mismatch")
	}
	if !bytes.Contains(rec, []byte("a")) || !bytes.Contains(rec, []byte("123")) {
		t.Errorf("record missing payload")
	}
}
