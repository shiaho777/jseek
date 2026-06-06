package bench

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/buger/jsonparser"
	"github.com/tidwall/gjson"
	"github.com/shiahonb777/jseek"
)

// This file validates the synthetic-fixture wins on JSON shaped like real APIs:
// a GitHub-style nested object response, and an NDJSON access-log stream. The
// goal is to confirm the index/tape advantages hold on representative data and
// access patterns, not just hand-tuned microbenchmarks.

// --- GitHub-style API response: deeply nested object with arrays ---

func makeGitHubResponse(numIssues int) []byte {
	var b strings.Builder
	b.WriteString(`{`)
	b.WriteString(`"id":1296269,"node_id":"MDEwOlJlcG9zaXRvcnkxMjk2MjY5","name":"Hello-World",`)
	b.WriteString(`"full_name":"octocat/Hello-World","private":false,`)
	b.WriteString(`"owner":{"login":"octocat","id":1,"node_id":"MDQ6VXNlcjE=","type":"User","site_admin":false,`)
	b.WriteString(`"avatar_url":"https://github.com/images/error/octocat_happy.gif","html_url":"https://github.com/octocat"},`)
	b.WriteString(`"html_url":"https://github.com/octocat/Hello-World","description":"This your first repo!",`)
	b.WriteString(`"fork":false,"url":"https://api.github.com/repos/octocat/Hello-World",`)
	b.WriteString(`"stargazers_count":80,"watchers_count":80,"forks_count":9,"open_issues_count":0,`)
	b.WriteString(`"license":{"key":"mit","name":"MIT License","spdx_id":"MIT","url":"https://api.github.com/licenses/mit"},`)
	b.WriteString(`"permissions":{"admin":false,"push":false,"pull":true},`)
	b.WriteString(`"topics":["octocat","atom","electron","api"],`)
	b.WriteString(`"issues":[`)
	for i := 0; i < numIssues; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":%d,"number":%d,"title":"Issue number %d","state":"open",`, 1000+i, i, i)
		fmt.Fprintf(&b, `"user":{"login":"user%d","id":%d,"type":"User"},`, i, 2000+i)
		fmt.Fprintf(&b, `"labels":[{"id":%d,"name":"bug","color":"f29513"},{"id":%d,"name":"help wanted","color":"159818"}],`, i, i+1)
		fmt.Fprintf(&b, `"comments":%d,"created_at":"2024-01-%02dT00:00:00Z","body":"This is the body of issue %d with some descriptive text."}`, i%30, (i%28)+1, i)
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

var githubFixture = makeGitHubResponse(200)

// Realistic access: pull a handful of fields from the repo header and a couple
// of nested issue fields — the kind of thing a webhook handler does.
var ghPathsJseek = [][]string{
	{"full_name"},
	{"owner", "login"},
	{"license", "spdx_id"},
	{"stargazers_count"},
	{"issues", "[0]", "title"},
	{"issues", "[100]", "user", "login"},
	{"issues", "[199]", "labels", "[1]", "name"},
}

var ghPathsGJSON = []string{
	"full_name", "owner.login", "license.spdx_id", "stargazers_count",
	"issues.0.title", "issues.100.user.login", "issues.199.labels.1.name",
}

func BenchmarkGitHub_JseekStateless(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, p := range ghPathsJseek {
			_, _ = jseek.GetBytes(githubFixture, p...)
		}
	}
}

func BenchmarkGitHub_JseekIndexTape(b *testing.B) {
	q := jseek.CompileStrings(ghPathsJseek...)
	d := jseek.IndexTape(githubFixture)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q.EachDoc(d, func(idx int, value []byte, vt jseek.ValueType, err error) {})
	}
}

// Per-request realistic: index the document once per request, read fields, free.
func BenchmarkGitHub_JseekPerRequest(b *testing.B) {
	q := jseek.CompileStrings(ghPathsJseek...)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		d := jseek.IndexTapePooled(githubFixture)
		q.EachDoc(d, func(idx int, value []byte, vt jseek.ValueType, err error) {})
		d.Free()
	}
}

func BenchmarkGitHub_GJSON(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = gjson.GetManyBytes(githubFixture, ghPathsGJSON...)
	}
}

func BenchmarkGitHub_JSONParser(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, p := range ghPathsJseek {
			_, _, _, _ = jsonparser.Get(githubFixture, p...)
		}
	}
}

// --- NDJSON access-log stream: many independent records ---

func makeNDJSONLogs(n int) []byte {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `{"ts":"2024-06-03T%02d:%02d:%02dZ","level":"info","method":"GET","path":"/api/v1/resource/%d",`,
			i%24, i%60, i%60, i)
		fmt.Fprintf(&b, `"status":%d,"latency_ms":%d,"bytes":%d,`, 200+(i%5)*100, i%500, i*128)
		fmt.Fprintf(&b, `"client":{"ip":"10.0.%d.%d","ua":"Mozilla/5.0","region":"us-east-1"},`, i%256, (i*7)%256)
		fmt.Fprintf(&b, `"trace_id":"%032x"}`, i)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

var ndjsonFixture = makeNDJSONLogs(5000)

// Realistic stream processing: for each log line, extract status, latency, and a
// nested client field. This is the canonical "tail and aggregate" workload.
func BenchmarkNDJSON_JseekStream(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var totalLatency int64
		var errors int
		dec := jseek.NewDecoder(bytes.NewReader(ndjsonFixture))
		_ = dec.ForEach(func(elem []byte) error {
			lat, _ := jseek.GetInt(elem, "latency_ms")
			totalLatency += lat
			if st, _ := jseek.GetInt(elem, "status"); st >= 500 {
				errors++
			}
			_, _ = jseek.GetStringUnsafe(elem, "client", "region")
			return nil
		})
		_ = totalLatency
		_ = errors
	}
}

// Same workload, reusing a single Document via Reset+tape per line (zero-alloc
// per record, O(1) nested access).
func BenchmarkNDJSON_JseekStreamReuse(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var totalLatency int64
		var doc jseek.Document
		dec := jseek.NewDecoder(bytes.NewReader(ndjsonFixture))
		_ = dec.ForEach(func(elem []byte) error {
			doc.Reset(elem)
			lat, _ := doc.GetInt("latency_ms")
			totalLatency += lat
			_, _ = doc.GetString("client", "region")
			return nil
		})
		_ = totalLatency
	}
}

func BenchmarkNDJSON_GJSON(b *testing.B) {
	lines := strings.Split(string(ndjsonFixture), "\n")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var totalLatency int64
		var errs int
		for _, line := range lines {
			if line == "" {
				continue
			}
			totalLatency += gjson.Get(line, "latency_ms").Int()
			if gjson.Get(line, "status").Int() >= 500 {
				errs++
			}
			_ = gjson.Get(line, "client.region").String()
		}
		_ = totalLatency
		_ = errs
	}
}

// In-memory fast path: StreamBytes over the whole slice, zero allocation, no
// reader. This is the apples-to-apples competitor to gjson's in-memory access.
func BenchmarkNDJSON_JseekStreamBytes(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var totalLatency int64
		var errs int
		_ = jseek.StreamBytes(ndjsonFixture, func(elem []byte) error {
			lat, _ := jseek.GetInt(elem, "latency_ms")
			totalLatency += lat
			if st, _ := jseek.GetInt(elem, "status"); st >= 500 {
				errs++
			}
			_, _ = jseek.GetStringUnsafe(elem, "client", "region")
			return nil
		})
		_ = totalLatency
		_ = errs
	}
}

// Single-pass multi-field read per record via a precompiled path set (no
// per-record index). Reads all three fields in one scan instead of three.
func BenchmarkNDJSON_JseekStreamBytesEachKey(b *testing.B) {
	q := jseek.CompileStrings(
		[]string{"latency_ms"},
		[]string{"status"},
		[]string{"client", "region"},
	)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var totalLatency int64
		var errs int
		_ = jseek.StreamBytes(ndjsonFixture, func(elem []byte) error {
			q.Each(elem, func(idx int, value []byte, vt jseek.ValueType, err error) {
				switch idx {
				case 0:
					if n, ok := (jseek.Result{Raw: value, Type: vt}).Int(); ok {
						totalLatency += n
					}
				case 1:
					if n, ok := (jseek.Result{Raw: value, Type: vt}).Int(); ok && n >= 500 {
						errs++
					}
				}
			})
			return nil
		})
		_ = totalLatency
		_ = errs
	}
}
