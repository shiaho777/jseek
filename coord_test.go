//go:build unix

package jseek

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"testing"
)

// TestWALWriteLockCrossProcess spawns a child process that holds the WAL write
// lock, then verifies the parent cannot acquire it (true cross-process flock).
func TestWALWriteLockCrossProcess(t *testing.T) {
	if os.Getenv("JSEEK_LOCK_CHILD") == "1" {
		// Child: open the WAL (takes the lock) and hold it until stdin closes.
		base := os.Getenv("JSEEK_LOCK_BASE")
		w, err := OpenWAL(base, base+".wal")
		if err != nil {
			os.Exit(3)
		}
		defer w.Close()
		os.Stdout.WriteString("locked\n")
		os.Stdout.Sync()
		buf := make([]byte, 1)
		os.Stdin.Read(buf) // block until parent closes stdin
		os.Exit(0)
	}
	base := sealTemp(t, `{"a":1}`)
	cmd := exec.Command(os.Args[0], "-test.run=TestWALWriteLockCrossProcess")
	cmd.Env = append(os.Environ(), "JSEEK_LOCK_CHILD=1", "JSEEK_LOCK_BASE="+base)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Wait for the child to signal it holds the lock.
	buf := make([]byte, 7)
	if _, err := stdout.Read(buf); err != nil {
		t.Fatal(err)
	}
	// Parent must be rejected while the child holds the lock.
	if _, err := OpenWAL(base, base+".wal"); err != ErrLocked {
		t.Fatalf("parent err=%v want ErrLocked while child holds lock", err)
	}
	// Release the child; parent can then acquire.
	stdin.Close()
	cmd.Wait()
	w, err := OpenWAL(base, base+".wal")
	if err != nil {
		t.Fatalf("after child exit: %v", err)
	}
	w.Close()
}

// TestWALWriteLockExclusion verifies a second writer on the same WAL is rejected.
func TestWALWriteLockExclusion(t *testing.T) {
	base := sealTemp(t, `{"a":1}`)
	walPath := base + ".wal"
	w1, err := OpenWAL(base, walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer w1.Close()
	// A second writer must fail with ErrLocked.
	_, err = OpenWAL(base, walPath)
	if err != ErrLocked {
		t.Fatalf("second writer err=%v want ErrLocked", err)
	}
	// After closing the first, a new writer succeeds.
	w1.Close()
	w2, err := OpenWAL(base, walPath)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	w2.Close()
}

// TestWALReaderIncrementalRefresh verifies a reader sees a writer's new commits
// via Refresh, without reopening.
func TestWALReaderIncrementalRefresh(t *testing.T) {
	base := sealTemp(t, `{"a":1,"b":2}`)
	walPath := base + ".wal"
	w, err := OpenWAL(base, walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	r, err := OpenWALReader(base, walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	// Initial: a=1.
	if v, err := r.GetInt("a"); err != nil || v != 1 {
		t.Fatalf("initial a=%d err=%v", v, err)
	}
	// Writer commits a=10.
	w.Set([]byte(`10`), "a")
	// Reader before refresh still sees 1 (snapshot).
	if v, _ := r.GetInt("a"); v != 1 {
		t.Fatalf("pre-refresh a=%d want 1", v)
	}
	// Refresh picks up the new edit incrementally.
	if err := r.Refresh(); err != nil {
		t.Fatal(err)
	}
	if v, err := r.GetInt("a"); err != nil || v != 10 {
		t.Fatalf("post-refresh a=%d err=%v want 10", v, err)
	}
	if r.Pending() != 1 {
		t.Fatalf("pending=%d want 1", r.Pending())
	}
	// Another commit + refresh.
	w.Set([]byte(`"hi"`), "c")
	r.Refresh()
	if v, vt, err := r.Get("c"); err != nil || vt != String || string(v) != "hi" {
		t.Fatalf("c=%q vt=%v err=%v", v, vt, err)
	}
	if r.Pending() != 2 {
		t.Fatalf("pending=%d want 2", r.Pending())
	}
}

// TestWALReaderAcrossCompact verifies the reader stays consistent when the writer
// compacts (base replaced, WAL truncated).
func TestWALReaderAcrossCompact(t *testing.T) {
	base := sealTemp(t, `{"a":1}`)
	walPath := base + ".wal"
	w, err := OpenWAL(base, walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	r, err := OpenWALReader(base, walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	w.Set([]byte(`42`), "a")
	r.Refresh()
	if v, _ := r.GetInt("a"); v != 42 {
		t.Fatalf("pre-compact a=%d want 42", v)
	}
	// Compact folds the edit into the base and truncates the WAL.
	if err := w.Compact(); err != nil {
		t.Fatal(err)
	}
	// Reader refresh detects the new base and replays the (empty) WAL.
	if err := r.Refresh(); err != nil {
		t.Fatal(err)
	}
	if v, err := r.GetInt("a"); err != nil || v != 42 {
		t.Fatalf("post-compact a=%d err=%v want 42", v, err)
	}
	if r.Pending() != 0 {
		t.Fatalf("pending=%d want 0 after compact", r.Pending())
	}
	// Writer continues; reader still tracks.
	w.Set([]byte(`99`), "a")
	r.Refresh()
	if v, _ := r.GetInt("a"); v != 99 {
		t.Fatalf("post-compact-write a=%d want 99", v)
	}
}

// TestWALReaderTornTailIgnored verifies a reader does not apply an incomplete
// trailing record (simulated crash mid-append).
func TestWALReaderTornTailIgnored(t *testing.T) {
	base := sealTemp(t, `{"a":1}`)
	walPath := base + ".wal"
	w, err := OpenWAL(base, walPath)
	if err != nil {
		t.Fatal(err)
	}
	w.Set([]byte(`5`), "a")
	w.Close()
	r, err := OpenWALReader(base, walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	r.Refresh()
	// Append a torn record directly.
	f, _ := os.OpenFile(walPath, os.O_WRONLY|os.O_APPEND, 0644)
	var hdr [8]byte
	hdr[0] = 'E'
	binary.LittleEndian.PutUint16(hdr[2:4], 1)
	binary.LittleEndian.PutUint32(hdr[4:8], 100)
	f.Write(hdr[:])
	f.Write([]byte("x"))
	f.Close()
	r.Refresh()
	if v, _ := r.GetInt("a"); v != 5 {
		t.Fatalf("a=%d want 5 (torn tail ignored)", v)
	}
	if r.Pending() != 1 {
		t.Fatalf("pending=%d want 1", r.Pending())
	}
}

func TestCheckVersion(t *testing.T) {
	if err := checkVersion(FormatStore, 1); err != nil {
		t.Errorf("current version: %v", err)
	}
	if err := checkVersion(FormatStore, 2); err != ErrUnsupportedVersion {
		t.Errorf("newer version err=%v want ErrUnsupportedVersion", err)
	}
	if err := checkVersion(FormatStore, 0); err != ErrUnsupportedVersion {
		t.Errorf("older version err=%v want ErrUnsupportedVersion", err)
	}
}

func TestNDJSONUnsupportedVersion(t *testing.T) {
	var buf bytes.Buffer
	SealNDJSON(&buf, bytes.NewBufferString("{\"a\":1}\n"))
	img := buf.Bytes()
	// Bump the version word to 2.
	binary.LittleEndian.PutUint32(img[8:12], 2)
	_, err := parseNDJSON(img)
	if err != ErrUnsupportedVersion {
		t.Fatalf("err=%v want ErrUnsupportedVersion", err)
	}
}

// TestMigrateChain exercises the migration registry with a synthetic v1->v2.
func TestMigrateChain(t *testing.T) {
	kind := FormatKind("TESTFMT")
	currentVersions[kind] = 2
	minVersions[kind] = 1
	defer func() {
		delete(currentVersions, kind)
		delete(minVersions, kind)
		migMu.Lock()
		delete(migrations, kind)
		migMu.Unlock()
	}()
	RegisterMigration(kind, 1, func(img []byte) ([]byte, error) {
		out := append([]byte(nil), img...)
		binary.LittleEndian.PutUint32(out[8:12], 2)
		return out, nil
	})
	img := make([]byte, 32)
	binary.LittleEndian.PutUint32(img[8:12], 1)
	out, err := Migrate(kind, img)
	if err != nil {
		t.Fatal(err)
	}
	if leUint32(out[8:12]) != 2 {
		t.Fatalf("migrated version=%d want 2", leUint32(out[8:12]))
	}
	// Already-current image passes through.
	out2, err := Migrate(kind, out)
	if err != nil || leUint32(out2[8:12]) != 2 {
		t.Fatalf("passthrough err=%v", err)
	}
	// Too-new image rejected.
	newer := make([]byte, 32)
	binary.LittleEndian.PutUint32(newer[8:12], 3)
	if _, err := Migrate(kind, newer); err != ErrUnsupportedVersion {
		t.Fatalf("newer err=%v want ErrUnsupportedVersion", err)
	}
}

func TestMigrateMissingHook(t *testing.T) {
	kind := FormatKind("TESTFMT2")
	currentVersions[kind] = 3
	minVersions[kind] = 1
	defer func() {
		delete(currentVersions, kind)
		delete(minVersions, kind)
	}()
	img := make([]byte, 32)
	binary.LittleEndian.PutUint32(img[8:12], 1)
	// No hooks registered: must fail rather than silently accept.
	if _, err := Migrate(kind, img); err != ErrUnsupportedVersion {
		t.Fatalf("err=%v want ErrUnsupportedVersion", err)
	}
}
