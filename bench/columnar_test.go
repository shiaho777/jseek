package bench

import (
	"fmt"
	"testing"

	"github.com/tidwall/gjson"
	"github.com/shiahonb777/jseek"
)

// Columnar vs row-wise repeated analytics over a homogeneous batch. This is the
// scenario jseek's Transpose targets: the SAME batch aggregated/scanned many
// times (dashboards, multi-metric jobs, repeated filtering). Transpose pays the
// JSON cost once; subsequent passes are native-slice scans.

func makeBatch(n int) [][]byte {
	out := make([][]byte, n)
	for i := 0; i < n; i++ {
		out[i] = []byte(fmt.Sprintf(
			`{"ts":%d,"level":"info","status":%d,"latency_ms":%d,"bytes":%d,"client":{"region":"us-east-1"}}`,
			1717200000+i, 200+(i%5)*100, i%500, i*128))
	}
	return out
}

var batch = makeBatch(5000)

const passes = 50

// Row-wise: re-navigate every record on every aggregation pass.
func BenchmarkColumnar_RowWise(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var total int64
		for p := 0; p < passes; p++ {
			var sum int64
			for _, rec := range batch {
				v, _ := jseek.GetInt(rec, "latency_ms")
				sum += v
			}
			total += sum
		}
		_ = total
	}
}

// Row-wise with gjson.
func BenchmarkColumnar_RowWiseGJSON(b *testing.B) {
	strs := make([]string, len(batch))
	for i, r := range batch {
		strs[i] = string(r)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var total int64
		for p := 0; p < passes; p++ {
			var sum int64
			for _, s := range strs {
				sum += gjson.Get(s, "latency_ms").Int()
			}
			total += sum
		}
		_ = total
	}
}

// Columnar: transpose once, then aggregate over the native slice.
func BenchmarkColumnar_Transposed(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		col := jseek.TransposeInt(batch, 0, "latency_ms")
		var total int64
		for p := 0; p < passes; p++ {
			var sum int64
			for _, v := range col {
				sum += v
			}
			total += sum
		}
		_ = total
	}
}
