package bench

// This file adds the two full-parser SOTA libraries to the comparison so jseek is
// measured against the real state of the art, not only against the other lazy
// extractors (jsonparser, gjson):
//
//   - bytedance/sonic   — JIT-accelerated; also exposes a lazy Get(path...) API.
//   - minio/simdjson-go — SIMD full parser; navigate the tape after Parse.
//
// IMPORTANT architecture caveat (read before trusting any number here):
//
//   * simdjson-go requires AVX2/SSE and runs on amd64 ONLY. On arm64 (e.g.
//     Apple silicon) simdjson.SupportedCPU() is false and these benchmarks
//     SKIP. Run them on an amd64 Linux host (the CI bench job) for real data.
//   * sonic's JIT is amd64-only too. On arm64 sonic falls back to a compat
//     path, so its arm64 numbers are NOT representative of sonic's fast path.
//     Treat sonic numbers as meaningful only from an amd64 run.
//
// Both are full parsers: sonic.Get copies the located value out, and simdjson
// must Parse the entire document before any lookup. The point of including them
// is to show the lazy-extraction tradeoff honestly across the whole field.

import (
	"testing"

	"github.com/bytedance/sonic"
	simdjson "github.com/minio/simdjson-go"
)

// ============================================================================
// sonic (lazy Get API). Path args: string = object key, int = array index.
// ============================================================================

func BenchmarkSmall_Sonic(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if n, err := sonic.Get(smallFixture, "uuid"); err == nil {
			_, _ = n.String()
		}
		if n, err := sonic.Get(smallFixture, "tz"); err == nil {
			_, _ = n.Int64()
		}
		if n, err := sonic.Get(smallFixture, "ua"); err == nil {
			_, _ = n.String()
		}
		if n, err := sonic.Get(smallFixture, "st"); err == nil {
			_, _ = n.Int64()
		}
	}
}

func BenchmarkLargeShallow_Sonic(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if n, err := sonic.Get(largeFixture, "meta", "version"); err == nil {
			_, _ = n.String()
		}
		if n, err := sonic.Get(largeFixture, "page", "total"); err == nil {
			_, _ = n.Int64()
		}
	}
}

func BenchmarkLargeIndexed_Sonic(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if n, err := sonic.Get(largeFixture, "users", 250, "username"); err == nil {
			_, _ = n.String()
		}
		if n, err := sonic.Get(largeFixture, "users", 250, "followers"); err == nil {
			_, _ = n.Int64()
		}
	}
}

// sonicQueryPaths mirrors queryPaths (12 scattered fields) with int indices.
var sonicQueryPaths = [][]interface{}{
	{"meta", "version"},
	{"meta", "source"},
	{"page", "total"},
	{"users", 0, "username"},
	{"users", 50, "followers"},
	{"users", 100, "name"},
	{"users", 250, "email"},
	{"users", 400, "following"},
	{"users", 499, "active"},
	{"users", 300, "avatar", "url"},
	{"trailer", "checksum"},
	{"trailer", "ok"},
}

func BenchmarkManyFields_Sonic(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, p := range sonicQueryPaths {
			if n, err := sonic.Get(largeFixture, p...); err == nil {
				_, _ = n.Raw()
			}
		}
	}
}

var sonicGHPaths = [][]interface{}{
	{"full_name"},
	{"owner", "login"},
	{"license", "spdx_id"},
	{"stargazers_count"},
	{"issues", 0, "title"},
	{"issues", 100, "user", "login"},
	{"issues", 199, "labels", 1, "name"},
}

func BenchmarkGitHub_Sonic(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, p := range sonicGHPaths {
			if n, err := sonic.Get(githubFixture, p...); err == nil {
				_, _ = n.Raw()
			}
		}
	}
}

// ============================================================================
// simdjson-go (SIMD full parser). amd64-only; skips on unsupported CPUs.
// FindElement navigates objects but NOT arrays, so only object-path scenarios
// are covered here. The reused *ParsedJson is the charitable (fastest) usage.
// ============================================================================

func BenchmarkSmall_Simdjson(b *testing.B) {
	if !simdjson.SupportedCPU() {
		b.Skip("simdjson-go requires AVX2/SSE (amd64); not supported on this CPU")
	}
	b.ReportAllocs()
	var pj *simdjson.ParsedJson
	var el simdjson.Element
	for i := 0; i < b.N; i++ {
		var err error
		pj, err = simdjson.Parse(smallFixture, pj)
		if err != nil {
			b.Fatal(err)
		}
		for _, key := range []string{"uuid", "tz", "ua", "st"} {
			it := pj.Iter()
			if e, err := it.FindElement(&el, key); err == nil {
				switch e.Type {
				case simdjson.TypeString:
					_, _ = e.Iter.String()
				default:
					_, _ = e.Iter.Int()
				}
			}
		}
	}
}

func BenchmarkLargeShallow_Simdjson(b *testing.B) {
	if !simdjson.SupportedCPU() {
		b.Skip("simdjson-go requires AVX2/SSE (amd64); not supported on this CPU")
	}
	b.ReportAllocs()
	var pj *simdjson.ParsedJson
	var el simdjson.Element
	for i := 0; i < b.N; i++ {
		var err error
		pj, err = simdjson.Parse(largeFixture, pj)
		if err != nil {
			b.Fatal(err)
		}
		it := pj.Iter()
		if e, err := it.FindElement(&el, "meta", "version"); err == nil {
			_, _ = e.Iter.String()
		}
		it = pj.Iter()
		if e, err := it.FindElement(&el, "page", "total"); err == nil {
			_, _ = e.Iter.Int()
		}
	}
}

// BenchmarkLargeParseOnly_Simdjson isolates the cost a full parser pays that a
// lazy extractor avoids: parsing the entire 24 KB document, before any lookup.
func BenchmarkLargeParseOnly_Simdjson(b *testing.B) {
	if !simdjson.SupportedCPU() {
		b.Skip("simdjson-go requires AVX2/SSE (amd64); not supported on this CPU")
	}
	b.ReportAllocs()
	var pj *simdjson.ParsedJson
	for i := 0; i < b.N; i++ {
		var err error
		pj, err = simdjson.Parse(largeFixture, pj)
		if err != nil {
			b.Fatal(err)
		}
	}
}
