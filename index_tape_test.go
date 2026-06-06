package jseek

import "testing"

func TestTapeMatchesNonTape(t *testing.T) {
	paths := [][]string{
		{"person", "name", "fullName"},
		{"person", "github", "followers"},
		{"person", "avatars", "[0]", "url"},
		{"company", "size"},
		{"tags", "[2]"},
		{"person", "score"},
		{"missing"},
		{"tags", "[9]"},
	}
	plain := Index(sample)
	taped := IndexTape(sample)
	for _, p := range paths {
		pv, pt, _, perr := plain.Get(p...)
		tv, tt, _, terr := taped.Get(p...)
		if (perr == nil) != (terr == nil) {
			t.Errorf("path %v: err mismatch plain=%v tape=%v", p, perr, terr)
			continue
		}
		if perr != nil {
			continue
		}
		if pt != tt || string(pv) != string(tv) {
			t.Errorf("path %v: plain=(%v,%q) tape=(%v,%q)", p, pt, pv, tt, tv)
		}
	}
}

func TestTapeRebuildOnReuse(t *testing.T) {
	d := IndexTape(sample)
	if s, _ := d.GetString("company", "name"); s != "Acme" {
		t.Fatalf("first: %q", s)
	}
	d.Reset([]byte(`{"company":{"name":"Other"}}`)).WithTape()
	if s, _ := d.GetString("company", "name"); s != "Other" {
		t.Fatalf("after reset: %q", s)
	}
}
