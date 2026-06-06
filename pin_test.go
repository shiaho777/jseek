package jseek

import "testing"

func TestPinBasic(t *testing.T) {
	d := Index(sample)
	p := d.Pin(
		[]string{"company", "name"},
		[]string{"person", "github", "followers"},
	)
	if v, vt, ok := p.Get(0); !ok || vt != String || string(v) != "Acme" {
		t.Fatalf("pin get 0: %q %v %v", v, vt, ok)
	}
	if v, vt, ok := p.Get(1); !ok || vt != Number || string(v) != "109" {
		t.Fatalf("pin get 1: %q %v %v", v, vt, ok)
	}
}

func TestPinDriftFallback(t *testing.T) {
	// Pin against one shape, then rebind to a DIFFERENT shape. The cache must
	// detect drift and still return correct values.
	d := Index([]byte(`{"a":1,"b":2,"c":3}`))
	p := d.Pin([]string{"a"}, []string{"c"})
	if v, _, ok := p.Get(0); !ok || string(v) != "1" {
		t.Fatalf("initial a: %q %v", v, ok)
	}

	// Rebind to a record where the keys moved (extra field, reorder).
	p.Rebind([]byte(`{"x":9,"c":30,"a":10,"b":20}`))
	if v, _, ok := p.Get(0); !ok || string(v) != "10" {
		t.Fatalf("drift a: got %q ok=%v (want 10)", v, ok)
	}
	if v, _, ok := p.Get(1); !ok || string(v) != "30" {
		t.Fatalf("drift c: got %q ok=%v (want 30)", v, ok)
	}
}

func TestPinHomogeneousStream(t *testing.T) {
	recs := makeHomogeneousRecords(50)
	d := Index(recs[0])
	p := d.Pin([]string{"status"}, []string{"latency_ms"})
	for i, rec := range recs {
		p.Rebind(rec)
		statusV, _, ok := p.Get(0)
		if !ok {
			t.Fatalf("record %d: status not found", i)
		}
		// Cross-check against an independent stateless Get.
		want, _, _, _ := Get(rec, "status")
		if string(statusV) != string(want) {
			t.Fatalf("record %d: pinned status %q != Get %q", i, statusV, want)
		}
	}
}
