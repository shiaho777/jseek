package jseek

import "testing"

func TestDocumentBasic(t *testing.T) {
	d := Index(sample)
	s, err := d.GetString("person", "name", "fullName")
	if err != nil || s != "Leonid Bugaev" {
		t.Fatalf("GetString: %q %v", s, err)
	}
	n, err := d.GetInt("person", "github", "followers")
	if err != nil || n != 109 {
		t.Fatalf("GetInt: %d %v", n, err)
	}
	f, err := d.GetFloat("person", "score")
	if err != nil || f != 98.6 {
		t.Fatalf("GetFloat: %v %v", f, err)
	}
	b, err := d.GetBoolean("person", "active")
	if err != nil || !b {
		t.Fatalf("GetBoolean: %v %v", b, err)
	}
}

func TestDocumentArrayIndex(t *testing.T) {
	d := Index(sample)
	s, err := d.GetString("person", "avatars", "[0]", "type")
	if err != nil || s != "thumbnail" {
		t.Fatalf("got %q %v", s, err)
	}
	s, err = d.GetString("tags", "[2]")
	if err != nil || s != "c" {
		t.Fatalf("got %q %v", s, err)
	}
}

func TestDocumentMatchesStatelessGet(t *testing.T) {
	d := Index(sample)
	paths := [][]string{
		{"person", "name", "first"},
		{"person", "name", "last"},
		{"person", "github", "handle"},
		{"person", "github", "followers"},
		{"person", "avatars", "[0]", "url"},
		{"person", "score"},
		{"person", "nickname"},
		{"company"},
		{"company", "name"},
		{"company", "size"},
		{"tags", "[0]"},
		{"tags", "[1]"},
		{"escaped"},
		{"missing"},
		{"person", "missing"},
		{"tags", "[9]"},
	}
	for _, p := range paths {
		wv, wt, _, werr := Get(sample, p...)
		gv, gt, _, gerr := d.Get(p...)
		if (werr == nil) != (gerr == nil) {
			t.Errorf("path %v: err mismatch stateless=%v indexed=%v", p, werr, gerr)
			continue
		}
		if werr != nil {
			continue
		}
		if wt != gt || string(wv) != string(gv) {
			t.Errorf("path %v: stateless=(%v,%q) indexed=(%v,%q)", p, wt, wv, gt, gv)
		}
	}
}

func TestDocumentNoKeys(t *testing.T) {
	d := Index(sample)
	_, vt, _, err := d.Get()
	if err != nil || vt != Object {
		t.Fatalf("top-level: %v %v", vt, err)
	}
}

func TestDocumentFreeAndPool(t *testing.T) {
	d := IndexPooled(sample)
	if _, err := d.GetString("company", "name"); err != nil {
		t.Fatal(err)
	}
	d.Free()
	// A fresh pooled document should still work after a Free returned a buffer.
	d2 := IndexPooled(sample)
	if s, err := d2.GetString("company", "name"); err != nil || s != "Acme" {
		t.Fatalf("after pool reuse: %q %v", s, err)
	}
	d2.Free()
}

func TestDocumentScalarTop(t *testing.T) {
	d := Index([]byte(`42`))
	v, vt, _, err := d.Get()
	if err != nil || vt != Number || string(v) != "42" {
		t.Fatalf("scalar top: %q %v %v", v, vt, err)
	}
	// Descending into a scalar must fail, matching stateless Get.
	if _, _, _, err := d.Get("a"); err == nil {
		t.Fatal("expected error descending into scalar")
	}
}
