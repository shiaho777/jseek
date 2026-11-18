package jseek

import (
	"bytes"
	"strings"
	"testing"
)

// patchAgree checks that a patched Document answers identically to a fresh
// IndexTape over the same new payload, across a spread of paths.
func patchAgree(t *testing.T, out []byte, nd *Document) {
	t.Helper()
	ref := IndexTape(out)
	paths := [][]string{
		nil, {"a"}, {"a", "b"}, {"users"}, {"users", "[0]"},
		{"users", "[0]", "name"}, {"users", "[1]", "name"},
		{"users", "[2]", "score"}, {"n"}, {"flag"}, {"missing"},
	}
	for _, p := range paths {
		a, at, _, ae := nd.Get(p...)
		b, bt, _, be := ref.Get(p...)
		if (ae == nil) != (be == nil) || at != bt || !bytes.Equal(a, b) {
			t.Fatalf("path=%v: patched=%q,%v,%v rebuild=%q,%v,%v", p, a, at, ae, b, bt, be)
		}
	}
	// Count agreement.
	for _, p := range [][]string{nil, {"users"}} {
		c1, e1 := nd.Count(p...)
		c2, e2 := ref.Count(p...)
		if c1 != c2 || (e1 == nil) != (e2 == nil) {
			t.Fatalf("Count(%v): patched=%d,%v rebuild=%d,%v", p, c1, e1, c2, e2)
		}
	}
}

func patchFixture() []byte {
	var b strings.Builder
	b.WriteString(`{"a":{"b":1},"n":10,"flag":true,"users":[`)
	for i := 0; i < 20; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"name":"u`)
		b.WriteString(itoa(i))
		b.WriteString(`","score":`)
		b.WriteString(itoa(i * 3))
		b.WriteString(`}`)
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

func TestPatchSet(t *testing.T) {
	data := patchFixture()
	base := Index(data)
	cases := []struct {
		val  string
		keys []string
	}{
		{`99`, []string{"n"}},
		{`"hello"`, []string{"a", "b"}},
		{`false`, []string{"flag"}},
		{`{"name":"new","score":999}`, []string{"users", "[5]"}},
		{`42`, []string{"added"}},          // insert new key
		{`"x"`, []string{"deep", "k"}},     // create nested
		{`[1,2,3]`, []string{"users"}},     // replace whole array
	}
	for _, c := range cases {
		out, nd, err := PatchSet(base, data, []byte(c.val), c.keys...)
		if err != nil {
			t.Fatalf("PatchSet(%v): %v", c.keys, err)
		}
		// Payload must equal plain Set.
		want, _ := Set(data, []byte(c.val), c.keys...)
		if !bytes.Equal(out, want) {
			t.Fatalf("PatchSet(%v) payload mismatch", c.keys)
		}
		patchAgree(t, out, nd)
	}
}

func TestPatchDelete(t *testing.T) {
	data := patchFixture()
	base := Index(data)
	for _, keys := range [][]string{
		{"n"}, {"flag"}, {"a", "b"}, {"users", "[3]"}, {"users", "[0]"},
		{"users", "[19]"}, {"missing"}, {"a"},
	} {
		out, nd, removed, err := PatchDelete(base, data, keys...)
		if err != nil {
			t.Fatalf("PatchDelete(%v): %v", keys, err)
		}
		want := Delete(data, keys...)
		if !bytes.Equal(out, want) {
			t.Fatalf("PatchDelete(%v) payload mismatch", keys)
		}
		_ = removed
		patchAgree(t, out, nd)
	}
}

func TestPatchChained(t *testing.T) {
	data := patchFixture()
	d := Index(data)
	cur := data
	// A chain of edits, each patching the previous result.
	edits := []struct {
		val  string
		keys []string
	}{
		{`1`, []string{"n"}},
		{`2`, []string{"n"}},
		{`"z"`, []string{"users", "[0]", "name"}},
		{`true`, []string{"flag"}},
		{`5`, []string{"added"}},
	}
	for _, e := range edits {
		out, nd, err := PatchSet(d, cur, []byte(e.val), e.keys...)
		if err != nil {
			t.Fatal(err)
		}
		patchAgree(t, out, nd)
		cur, d = out, nd
	}
	// Then a delete on the chained result.
	out, nd, _, err := PatchDelete(d, cur, "users", "[1]")
	if err != nil {
		t.Fatal(err)
	}
	patchAgree(t, out, nd)
}

// FuzzPatchMatchesRebuild is the correctness gate: for arbitrary valid JSON and
// a random edit, the patched index must answer exactly like a full rebuild.
func FuzzPatchMatchesRebuild(f *testing.F) {
	f.Add([]byte(`{"a":1,"b":[1,2,3],"c":"x"}`), "a", `9`)
	f.Add([]byte(`{"users":[{"n":1},{"n":2}]}`), "users", `[{"n":9}]`)
	f.Add([]byte(`{"k":{"deep":true}}`), "k", `null`)
	f.Fuzz(func(t *testing.T, data []byte, key, val string) {
		if !Valid(data) || !Valid([]byte(val)) {
			return
		}
		if key == "" || strings.ContainsAny(key, ".[]\"\\") {
			return
		}
		base := Index(data)
		out, nd, err := PatchSet(base, data, []byte(val), key)
		if err != nil {
			return
		}
		ref := IndexTape(out)
		// Compare top-level and the edited key and a few structural probes.
		for _, p := range [][]string{nil, {key}} {
			a, at, _, ae := nd.Get(p...)
			b, bt, _, be := ref.Get(p...)
			if (ae == nil) != (be == nil) || at != bt || !bytes.Equal(a, b) {
				t.Fatalf("PatchSet key=%q path=%v: patched=%q,%v,%v rebuild=%q,%v,%v",
					key, p, a, at, ae, b, bt, be)
			}
		}
		// Delete the same key from the original; compare.
		dout, dnd, _, derr := PatchDelete(base, data, key)
		if derr == nil {
			dref := IndexTape(dout)
			a, at, _, ae := dnd.Get()
			b, bt, _, be := dref.Get()
			if (ae == nil) != (be == nil) || at != bt || !bytes.Equal(a, b) {
				t.Fatalf("PatchDelete key=%q root mismatch", key)
			}
		}
	})
}

// BenchmarkPatchVsRebuild measures a small field edit on a large document:
// incremental patch vs full re-index.
func BenchmarkPatchVsRebuild(b *testing.B) {
	var sb strings.Builder
	sb.WriteString(`{"meta":{"v":1},"items":[`)
	for i := 0; i < 2000; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(`{"id":`)
		sb.WriteString(itoa(i))
		sb.WriteString(`,"name":"item_`)
		sb.WriteString(itoa(i))
		sb.WriteString(`","active":true}`)
	}
	sb.WriteString(`]}`)
	data := []byte(sb.String())
	base := Index(data)
	b.Run("FullRebuild", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			out, _ := Set(data, []byte(`2`), "meta", "v")
			d := Index(out)
			if _, err := d.GetInt("meta", "v"); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("IncrementalPatch", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			out, d, err := PatchSet(base, data, []byte(`2`), "meta", "v")
			if err != nil {
				b.Fatal(err)
			}
			if _, err := d.GetInt("meta", "v"); err != nil {
				b.Fatal(err)
			}
			_ = out
		}
	})
}