package jseek

import "testing"

func TestGetMany(t *testing.T) {
	results := GetMany(sample,
		[]string{"person", "name", "fullName"},
		[]string{"person", "github", "followers"},
		[]string{"person", "score"},
		[]string{"person", "active"},
		[]string{"does", "not", "exist"},
	)
	if len(results) != 5 {
		t.Fatalf("expected 5 results, got %d", len(results))
	}

	if got := results[0].String(); got != "Leonid Bugaev" {
		t.Errorf("result 0: %q", got)
	}
	if n, ok := results[1].Int(); !ok || n != 109 {
		t.Errorf("result 1: %d %v", n, ok)
	}
	if f, ok := results[2].Float(); !ok || f != 98.6 {
		t.Errorf("result 2: %v %v", f, ok)
	}
	if b, ok := results[3].Bool(); !ok || !b {
		t.Errorf("result 3: %v %v", b, ok)
	}
	if results[4].Exists() {
		t.Errorf("result 4 should not exist")
	}
}

func TestResultTypeMismatch(t *testing.T) {
	results := GetMany(sample, []string{"person", "name", "fullName"})
	// A string value asked for as Int should report not-ok, not panic.
	if _, ok := results[0].Int(); ok {
		t.Error("expected Int() to fail on a string value")
	}
	if _, ok := results[0].Bool(); ok {
		t.Error("expected Bool() to fail on a string value")
	}
}

func TestPathsGetManyReuse(t *testing.T) {
	q := CompileStrings(
		[]string{"company", "name"},
		[]string{"company", "size"},
	)
	r1 := q.GetMany(sample)
	r2 := q.GetMany([]byte(`{"company":{"name":"Other","size":7}}`))
	if r1[0].String() != "Acme" {
		t.Errorf("r1: %q", r1[0].String())
	}
	if r2[0].String() != "Other" {
		t.Errorf("r2: %q", r2[0].String())
	}
	if n, _ := r2[1].Int(); n != 7 {
		t.Errorf("r2 size: %d", n)
	}
}
