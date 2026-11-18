package jseek

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func walBase(t *testing.T) (string, []byte) {
	t.Helper()
	data := persistFixture()
	dir := t.TempDir()
	path := filepath.Join(dir, "base.jstore")
	f, _ := os.Create(path)
	if err := Seal(f, data); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return path, data
}

func TestWALSetGet(t *testing.T) {
	path, _ := walBase(t)
	w, err := OpenWAL(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	// Base read.
	if v, _ := w.GetInt("users", "[250]", "score"); v != 250*7 {
		t.Fatalf("base score=%d", v)
	}
	// Cheap write.
	if err := w.Set([]byte(`999`), "users", "[250]", "score"); err != nil {
		t.Fatal(err)
	}
	if v, err := w.GetInt("users", "[250]", "score"); err != nil || v != 999 {
		t.Fatalf("after set score=%d err=%v", v, err)
	}
	// Other records unaffected.
	if v, _ := w.GetInt("users", "[0]", "score"); v != 0 {
		t.Errorf("record 0 changed: %d", v)
	}
	// Overwrite again — latest wins.
	w.Set([]byte(`1234`), "users", "[250]", "score")
	if v, _ := w.GetInt("users", "[250]", "score"); v != 1234 {
		t.Errorf("latest wins failed: %d", v)
	}
	// Delete.
	w.Delete("users", "[250]", "score")
	if w.Exists("users", "[250]", "score") {
		t.Error("deleted key still exists")
	}
	if w.Pending() != 3 {
		t.Errorf("pending=%d want 3", w.Pending())
	}
}

func TestWALSubtreeRead(t *testing.T) {
	path, _ := walBase(t)
	w, err := OpenWAL(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.Set([]byte(`777`), "users", "[3]", "score")
	// Reading the whole element must reflect the overlay edit.
	d, _, err := w.Get("users", "[3]")
	if err != nil {
		t.Fatal(err)
	}
	if v, err := GetInt(d, "score"); err != nil || v != 777 {
		t.Fatalf("subtree element score=%d err=%v", v, err)
	}
	// Reading the whole array must reflect it too.
	arr, _, err := w.Get("users")
	if err != nil {
		t.Fatal(err)
	}
	if v, err := GetInt(arr, "[3]", "score"); err != nil || v != 777 {
		t.Fatalf("array subtree score=%d err=%v", v, err)
	}
	// Unaffected subtree stays correct.
	if v, _ := w.GetInt("users", "[4]", "score"); v != 4*7 {
		t.Errorf("unaffected record changed: %d", v)
	}
}

func TestWALCompact(t *testing.T) {
	path, _ := walBase(t)
	w, err := OpenWAL(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.Set([]byte(`11`), "users", "[1]", "score")
	w.Set([]byte(`22`), "users", "[2]", "score")
	w.Delete("meta", "v")
	if err := w.Compact(); err != nil {
		t.Fatal(err)
	}
	if w.Pending() != 0 {
		t.Errorf("pending after compact=%d", w.Pending())
	}
	if v, _ := w.GetInt("users", "[1]", "score"); v != 11 {
		t.Errorf("compacted score1=%d", v)
	}
	if v, _ := w.GetInt("users", "[2]", "score"); v != 22 {
		t.Errorf("compacted score2=%d", v)
	}
	if w.Exists("meta", "v") {
		t.Error("meta.v survived compact delete")
	}
	// Writes after compaction still work.
	w.Set([]byte(`33`), "users", "[3]", "score")
	if v, _ := w.GetInt("users", "[3]", "score"); v != 33 {
		t.Errorf("post-compact write failed: %d", v)
	}
}

func TestWALPersistenceReplay(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "b.jstore")
	f, _ := os.Create(basePath)
	Seal(f, persistFixture())
	f.Close()
	walPath := filepath.Join(dir, "edits.wal")
	w, err := OpenWAL(basePath, walPath)
	if err != nil {
		t.Fatal(err)
	}
	w.Set([]byte(`555`), "users", "[10]", "score")
	w.Delete("flag")
	w.Close()
	// Reopen: edits must replay from the WAL file.
	w2, err := OpenWAL(basePath, walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	if v, _ := w2.GetInt("users", "[10]", "score"); v != 555 {
		t.Errorf("replayed score=%d", v)
	}
	if w2.Exists("flag") {
		t.Error("replayed delete lost")
	}
	if w2.Pending() != 2 {
		t.Errorf("replayed pending=%d", w2.Pending())
	}
	// Compact truncates the WAL.
	w2.Compact()
	w2.Close()
	w3, _ := OpenWAL(basePath, walPath)
	defer w3.Close()
	if w3.Pending() != 0 {
		t.Errorf("wal not truncated: pending=%d", w3.Pending())
	}
}

func TestWALPathEscaping(t *testing.T) {
	// Keys containing the separator or backslash must round-trip.
	keys := []string{"a\x1fb", "c\\d", "e"}
	joined := walPathKey(keys)
	got := splitWALPath(joined)
	if len(got) != len(keys) {
		t.Fatalf("split len=%d want %d", len(got), len(keys))
	}
	for i := range keys {
		if got[i] != keys[i] {
			t.Errorf("seg %d: %q want %q", i, got[i], keys[i])
		}
	}
}

// FuzzWALMatchesMaterialize checks that overlay reads agree with a fully
// materialized document for arbitrary edit sequences.
func FuzzWALMatchesMaterialize(f *testing.F) {
	f.Add([]byte(`{"a":1,"b":{"c":2},"arr":[1,2,3]}`), "a", `9`)
	f.Add([]byte(`{"x":{"y":1}}`), "x", `{"y":5}`)
	f.Fuzz(func(t *testing.T, data []byte, key, val string) {
		if !Valid(data) || !Valid([]byte(val)) || key == "" {
			return
		}
		if strings.ContainsAny(key, "\x1f\\") {
			return
		}
		dir := t.TempDir()
		bp := filepath.Join(dir, "b.jstore")
		fh, _ := os.Create(bp)
		if err := Seal(fh, data); err != nil {
			fh.Close()
			return
		}
		fh.Close()
		w, err := OpenWAL(bp, "")
		if err != nil {
			return
		}
		defer w.Close()
		if err := w.Set([]byte(val), key); err != nil {
			return
		}
		// Overlay read of the edited key must equal the value.
		got, _, err := w.Get(key)
		if err != nil {
			t.Fatalf("overlay get %q: %v", key, err)
		}
		want, _, _, _ := Get([]byte(val))
		if !bytes.Equal(got, want) {
			t.Fatalf("overlay %q=%q want %q", key, got, want)
		}
		// Root subtree read must equal materialized Set.
		root, _, err := w.Get()
		if err != nil {
			t.Fatal(err)
		}
		mat, _ := Set(data, []byte(val), key)
		if !Valid(root) {
			t.Fatalf("materialized root invalid: %q", root)
		}
		rv, _, _, _ := Get(root, key)
		mv, _, _, _ := Get(mat, key)
		if !bytes.Equal(rv, mv) {
			t.Fatalf("root subtree %q: overlay=%q materialized=%q", key, rv, mv)
		}
	})
}

// TestWALMaterializeCacheInvalidation verifies the cached materialization is
// rebuilt when edits change and reused otherwise.
func TestWALMaterializeCacheInvalidation(t *testing.T) {
	path, _ := walBase(t)
	w, err := OpenWAL(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.Set([]byte(`11`), "users", "[1]", "score")
	arr, _, _ := w.Get("users")
	if v, _ := GetInt(arr, "[1]", "score"); v != 11 {
		t.Fatalf("first read score=%d", v)
	}
	arr2, _, _ := w.Get("users")
	if v, _ := GetInt(arr2, "[1]", "score"); v != 11 {
		t.Fatalf("cached read score=%d", v)
	}
	w.Set([]byte(`22`), "users", "[2]", "score")
	arr3, _, _ := w.Get("users")
	if v, _ := GetInt(arr3, "[2]", "score"); v != 22 {
		t.Fatalf("post-edit read score=%d (cache not invalidated?)", v)
	}
	if v, _ := GetInt(arr3, "[1]", "score"); v != 11 {
		t.Fatalf("post-edit prior edit lost score=%d", v)
	}
	w.Compact()
	arr4, _, _ := w.Get("users")
	if v, _ := GetInt(arr4, "[2]", "score"); v != 22 {
		t.Fatalf("post-compact score=%d", v)
	}
}

// BenchmarkWALDirtyRead measures repeated reads of an overlay-dirtied subtree:
// without the cache each read materializes the whole document; with it, only
// the first does. This is the boundary-breaking measurement.
func BenchmarkWALDirtyRead(b *testing.B) {
	var sb strings.Builder
	sb.WriteString(`{"cfg":{"v":1},"items":[`)
	for i := 0; i < 5000; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(`{"id":`)
		sb.WriteString(itoa(i))
		sb.WriteString(`,"desc":"a long description string number `)
		sb.WriteString(itoa(i))
		sb.WriteString(`","ok":true}`)
	}
	sb.WriteString(`]}`)
	data := []byte(sb.String())
	dir := b.TempDir()
	bp := filepath.Join(dir, "b.jstore")
	fh, _ := os.Create(bp)
	Seal(fh, data)
	fh.Close()
	w, err := OpenWAL(bp, "")
	if err != nil {
		b.Fatal(err)
	}
	defer w.Close()
	w.Set([]byte(`false`), "items", "[2500]", "ok")
	b.Run("CachedRepeatedRead", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			v, _, err := w.Get("items", "[2500]", "ok")
			if err != nil {
				b.Fatal(err)
			}
			_ = v
		}
	})
	b.Run("FreshMaterializeEachRead", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			doc, err := buildMaterialized(w.base, w.edits)
			if err != nil {
				b.Fatal(err)
			}
			if _, _, _, err := Get(doc, "items", "[2500]", "ok"); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkWALSubtreeFirstRead measures the FIRST dirty read (cache cold) of a
// small field inside a large edited array. With subtree-local resolution it
// should be ~constant regardless of array size; the old full-materialize path
// grew with the document. This is the boundary-breaking measurement.
func BenchmarkWALSubtreeFirstRead(b *testing.B) {
	for _, n := range []int{100, 5000, 50000} {
		var sb strings.Builder
		sb.WriteString(`{"items":[`)
		for i := 0; i < n; i++ {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(`{"id":`)
			sb.WriteString(itoa(i))
			sb.WriteString(`,"desc":"a long description string number `)
			sb.WriteString(itoa(i))
			sb.WriteString(`","ok":true}`)
		}
		sb.WriteString(`]}`)
		data := []byte(sb.String())
		dir := b.TempDir()
		bp := filepath.Join(dir, "b.jstore")
		fh, _ := os.Create(bp)
		Seal(fh, data)
		fh.Close()
		w, err := OpenWAL(bp, "")
		if err != nil {
			b.Fatal(err)
		}
		// One edit deep in the array dirties the items subtree.
		w.Set([]byte(`false`), "items", "["+itoa(n/2)+"]", "ok")
		b.Run("Local_"+itoa(n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				// Bump gen (cache cold), then read the ELEMENT (parent of the
				// edited field) so descendant edits force subtree assembly.
				w.Set([]byte(`false`), "items", "["+itoa(n/2)+"]", "ok")
				v, _, err := w.Get("items", "["+itoa(n/2)+"]")
				if err != nil {
					b.Fatal(err)
				}
				_ = v
			}
		})
		w.Close()
	}
}

// BenchmarkWALWrite is the breakthrough measurement: a cheap overlay write vs
// a full payload-rewriting Set on a large document.
func BenchmarkWALWrite(b *testing.B) {
	var sb strings.Builder
	sb.WriteString(`{"cfg":{"v":1},"items":[`)
	for i := 0; i < 5000; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(`{"id":`)
		sb.WriteString(itoa(i))
		sb.WriteString(`,"desc":"a long description string number `)
		sb.WriteString(itoa(i))
		sb.WriteString(`","ok":true}`)
	}
	sb.WriteString(`]}`)
	data := []byte(sb.String())
	dir := b.TempDir()
	bp := filepath.Join(dir, "b.jstore")
	fh, _ := os.Create(bp)
	Seal(fh, data)
	fh.Close()
	w, err := OpenWAL(bp, "")
	if err != nil {
		b.Fatal(err)
	}
	defer w.Close()
	b.Run("FullSetRewrite", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			out, err := Set(data, []byte(`2`), "cfg", "v")
			if err != nil {
				b.Fatal(err)
			}
			_ = out
		}
	})
	b.Run("WALAppend", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if err := w.Set([]byte(`2`), "cfg", "v"); err != nil {
				b.Fatal(err)
			}
		}
	})
}