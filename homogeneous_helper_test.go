package jseek

import "fmt"

// makeHomogeneousRecords builds n log records of identical structure (with
// varying values), modeling a homogeneous NDJSON stream. Shared by pin and
// column tests.
func makeHomogeneousRecords(n int) [][]byte {
	out := make([][]byte, n)
	for i := 0; i < n; i++ {
		out[i] = []byte(fmt.Sprintf(
			`{"ts":"2024-06-03T12:00:%02dZ","level":"info","method":"GET","path":"/api/v1/resource/%d","status":%d,"latency_ms":%d,"bytes":%d,"client":{"ip":"10.0.0.%d","region":"us-east-1"},"trace_id":"%032x"}`,
			i%60, i, 200+(i%5)*100, i%500, i*128, i%256, i))
	}
	return out
}
