package bench

import (
	"testing"

	"github.com/shiahonb777/jseek"
)

// Isolates Stage-1 index build cost (the gate on every indexed/tape/columnar
// path). largeFixture ~24KB, githubFixture ~60KB.
func BenchmarkIndexBuild_Large(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		d := jseek.IndexPooled(largeFixture)
		d.Free()
	}
}

func BenchmarkIndexBuild_GitHub(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		d := jseek.IndexPooled(githubFixture)
		d.Free()
	}
}
