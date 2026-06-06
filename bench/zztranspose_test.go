package bench

import (
	"testing"

	"github.com/shiaho777/jseek"
)

func BenchmarkTransposeBuildOnly(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = jseek.TransposeInt(batch, 0, "latency_ms")
	}
}
