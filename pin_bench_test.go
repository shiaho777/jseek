package jseek

import (
	"testing"
)

// EXPERIMENT (white-box): the real ceiling. When the SAME data is queried many
// times, the per-query cost should approach the pure lookup floor (Traj_C),
// because the structural index + learned trajectory are built ONCE and reused.
//
// We model a config/reference-data workload: one ~24KB document, queried N
// times for a handful of fields (as a hot config lookup would be). We compare:
//   A) cold: stateless Get every time (what gjson/jsonparser force)
//   B) indexed-reused: Index once, Document.Get many (current jseek best)
//   C) pinned-trajectory: Index once + learn colon trajectory once, then every
//      query is a direct structural-index read with zero search
//
// This is where "thousands of times" either appears or is exposed as fantasy.

func makePinFixture() []byte {
	// A config-like nested document (~24KB) with a stable shape.
	return largeConfigFixture
}

var largeConfigFixture = func() []byte {
	// Reuse a realistic medium doc: build once.
	var b []byte
	b = append(b, '{')
	b = append(b, []byte(`"service":{"name":"api-gateway","version":"4.2.1","region":"us-east-1"},`)...)
	b = append(b, []byte(`"limits":{"rps":10000,"burst":20000,"timeout_ms":3000},`)...)
	b = append(b, []byte(`"features":{"auth":true,"cache":true,"tracing":false},`)...)
	// padding array to push size up so scanning cost is non-trivial
	b = append(b, []byte(`"routes":[`)...)
	for i := 0; i < 300; i++ {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, []byte(`{"path":"/v1/resource/`)...)
		b = append(b, []byte{byte('0' + i%10)}...)
		b = append(b, []byte(`","method":"GET","upstream":"backend-`)...)
		b = append(b, []byte{byte('0' + i%10)}...)
		b = append(b, []byte(`.internal","weight":100,"healthy":true}`)...)
	}
	b = append(b, ']')
	b = append(b, '}')
	return b
}()

var pinPaths = [][]string{
	{"service", "region"},
	{"limits", "rps"},
	{"features", "cache"},
}

// A) Cold: stateless re-scan every query.
func BenchmarkPin_A_Cold(b *testing.B) {
	data := makePinFixture()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _, _ = Get(data, "service", "region")
		_, _, _, _ = Get(data, "limits", "rps")
		_, _, _, _ = Get(data, "features", "cache")
	}
}

// B) Indexed, reused: build index once, navigate per query.
func BenchmarkPin_B_IndexedReused(b *testing.B) {
	data := makePinFixture()
	d := IndexTape(data)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _, _ = d.Get("service", "region")
		_, _, _, _ = d.Get("limits", "rps")
		_, _, _, _ = d.Get("features", "cache")
	}
}

// C) Pinned: production Pin API — learn trajectory once, verified direct reads.
func BenchmarkPin_C_PinnedTrajectory(b *testing.B) {
	data := makePinFixture()
	d := Index(data)
	p := d.Pin(pinPaths...)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = p.Get(0)
		_, _, _ = p.Get(1)
		_, _, _ = p.Get(2)
	}
}
