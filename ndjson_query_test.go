package jseek

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"testing"
)

func buildNDJSON(t *testing.T, records []string) *NDJSONStore {
	t.Helper()
	var buf bytes.Buffer
	n, err := SealNDJSON(&buf, bytes.NewBufferString(joinLines(records)))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if n != len(records) {
		t.Fatalf("n=%d want %d", n, len(records))
	}
	s, err := openNDJSONImage(buf.Bytes())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func joinLines(rs []string) string {
	var b bytes.Buffer
	for _, r := range rs {
		b.WriteString(r)
		b.WriteByte('\n')
	}
	return b.String()
}

// openNDJSONImage parses an in-memory image without mmap (test helper).
func openNDJSONImage(img []byte) (*NDJSONStore, error) {
	s, err := parseNDJSON(img)
	if err != nil {
		return nil, err
	}
	s.image = img
	return s, nil
}

func TestNDJSONCountWhere(t *testing.T) {
	s := buildNDJSON(t, []string{
		`{"id":1,"lat":10,"status":"ok"}`,
		`{"id":2,"lat":200,"status":"error"}`,
		`{"id":3,"lat":150,"status":"error"}`,
		`{"id":4,"lat":5,"status":"ok"}`,
	})
	cases := []struct {
		where string
		want  int
	}{
		{"", 4},
		{`status == "error"`, 2},
		{`lat > 100`, 2},
		{`lat > 100 && status == "error"`, 2},
		{`lat >= 10 && lat <= 150`, 2},
		{`status != "ok"`, 2},
		{`id == 99`, 0},
	}
	for _, c := range cases {
		w, err := ParseWhere(c.where)
		if err != nil {
			t.Fatalf("parse %q: %v", c.where, err)
		}
		got, err := s.Count(w)
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		if got != c.want {
			t.Errorf("where %q: got %d want %d", c.where, got, c.want)
		}
	}
}

func TestNDJSONAggregate(t *testing.T) {
	s := buildNDJSON(t, []string{
		`{"lat":10,"status":"ok"}`,
		`{"lat":200,"status":"error"}`,
		`{"lat":150,"status":"error"}`,
		`{"lat":5,"status":"ok"}`,
		`{"status":"ok"}`, // missing lat
	})
	w, _ := ParseWhere(`status == "error"`)
	a, err := s.Aggregate(w, "lat")
	if err != nil {
		t.Fatal(err)
	}
	if a.Count != 2 || a.N != 2 || a.Sum != 350 || a.Min != 150 || a.Max != 200 || a.Avg() != 175 {
		t.Errorf("filtered agg: %+v", a)
	}
	all, err := s.Aggregate(nil, "lat")
	if err != nil {
		t.Fatal(err)
	}
	if all.Count != 5 || all.N != 4 || all.Sum != 365 || all.Min != 5 || all.Max != 200 {
		t.Errorf("all agg: %+v", all)
	}
}

func TestNDJSONGroupAggregate(t *testing.T) {
	s := buildNDJSON(t, []string{
		`{"region":"us","lat":10}`,
		`{"region":"us","lat":30}`,
		`{"region":"eu","lat":100}`,
		`{"region":"eu","lat":200}`,
		`{"region":"eu","lat":300}`,
	})
	g, err := s.GroupAggregate(nil, "region", "lat")
	if err != nil {
		t.Fatal(err)
	}
	if len(g) != 2 {
		t.Fatalf("groups=%d", len(g))
	}
	if g["us"].Sum != 40 || g["us"].N != 2 || g["us"].Avg() != 20 {
		t.Errorf("us: %+v", g["us"])
	}
	if g["eu"].Sum != 600 || g["eu"].N != 3 || g["eu"].Max != 300 {
		t.Errorf("eu: %+v", g["eu"])
	}
}

func TestNDJSONSelectProjection(t *testing.T) {
	s := buildNDJSON(t, []string{
		`{"id":1,"lat":10,"status":"ok"}`,
		`{"id":2,"lat":200,"status":"error"}`,
		`{"id":3,"lat":150,"status":"error"}`,
	})
	w, _ := ParseWhere(`lat > 100`)
	out, err := s.SelectJSON(w, []string{"id", "status"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"id\":2,\"status\":\"error\"}\n{\"id\":3,\"status\":\"error\"}\n"
	if string(out) != want {
		t.Errorf("projection:\ngot  %q\nwant %q", out, want)
	}
}

// TestNDJSONQueryDifferential checks Count/Aggregate against an independent
// encoding/json reference over generated records.
func TestNDJSONQueryDifferential(t *testing.T) {
	var records []string
	for i := 0; i < 500; i++ {
		lat := (i * 37) % 500
		status := "ok"
		if i%3 == 0 {
			status = "error"
		}
		region := "us"
		if i%2 == 0 {
			region = "eu"
		}
		records = append(records, `{"id":`+strconv.Itoa(i)+`,"lat":`+strconv.Itoa(lat)+`,"status":"`+status+`","region":"`+region+`"}`)
	}
	s := buildNDJSON(t, records)

	// Reference filter: lat > 100 && status == "error".
	refCount := 0
	refSum, refMin, refMax := 0.0, math.Inf(1), math.Inf(-1)
	refN := 0
	for _, r := range records {
		var m map[string]any
		if err := json.Unmarshal([]byte(r), &m); err != nil {
			t.Fatal(err)
		}
		lat, _ := m["lat"].(float64)
		st, _ := m["status"].(string)
		if lat > 100 && st == "error" {
			refCount++
			refN++
			refSum += lat
			if lat < refMin {
				refMin = lat
			}
			if lat > refMax {
				refMax = lat
			}
		}
	}
	w, _ := ParseWhere(`lat > 100 && status == "error"`)
	a, err := s.Aggregate(w, "lat")
	if err != nil {
		t.Fatal(err)
	}
	if a.Count != refCount || a.N != refN || a.Sum != refSum || a.Min != refMin || a.Max != refMax {
		t.Errorf("diff agg: got %+v want count=%d n=%d sum=%v min=%v max=%v", a, refCount, refN, refSum, refMin, refMax)
	}
}

func TestParseWhereErrors(t *testing.T) {
	for _, bad := range []string{`lat >`, `== 5`, `lat @@ 5`, `lat > abc`} {
		if _, err := ParseWhere(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestNDJSONSelectLimit(t *testing.T) {
	s := buildNDJSON(t, []string{`{"a":1}`, `{"a":2}`, `{"a":3}`})
	out, err := s.SelectJSON(nil, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "{\"a\":1}\n{\"a\":2}\n" {
		t.Errorf("limit: %q", out)
	}
}

// FuzzParseWhereNoPanic ensures the WHERE parser never panics on arbitrary input.
func FuzzParseWhereNoPanic(f *testing.F) {
	f.Add(`lat > 100 && status == "error"`)
	f.Add(`a.b.c <= -3.5`)
	f.Add(`x != null`)
	f.Fuzz(func(t *testing.T, s string) {
		w, err := ParseWhere(s)
		if err != nil {
			return
		}
		s2 := buildNDJSON(t, []string{`{"a":1,"b":"x","lat":50,"status":"ok"}`})
		_, _ = s2.Count(w)
	})
}

// FuzzNDJSONQueryDifferential checks Count against an independent reference.
func FuzzNDJSONQueryDifferential(f *testing.F) {
	f.Add([]byte("{\"v\":1}\n{\"v\":2}\n{\"v\":3}"), 2)
	f.Fuzz(func(t *testing.T, data []byte, threshold int) {
		var records []string
		for _, line := range bytes.Split(data, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			var m map[string]any
			if err := json.Unmarshal(line, &m); err != nil {
				continue
			}
			if _, ok := m["v"].(float64); !ok {
				continue
			}
			records = append(records, string(line))
		}
		if len(records) == 0 || len(records) > 200 {
			return
		}
		s := buildNDJSON(t, records)
		w, err := ParseWhere(`v > ` + strconv.Itoa(threshold))
		if err != nil {
			return
		}
		got, err := s.Count(w)
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		for _, r := range records {
			var m map[string]any
			json.Unmarshal([]byte(r), &m)
			if m["v"].(float64) > float64(threshold) {
				want++
			}
		}
		if got != want {
			t.Fatalf("count got=%d want=%d threshold=%d", got, want, threshold)
		}
	})
}

func benchmarkQueryStore(b *testing.B, n int) *NDJSONStore {
	var buf bytes.Buffer
	for i := 0; i < n; i++ {
		lat := (i * 37) % 500
		st := "ok"
		if i%3 == 0 {
			st = "error"
		}
		buf.WriteString(`{"id":` + strconv.Itoa(i) + `,"lat":` + strconv.Itoa(lat) + `,"status":"` + st + `"}` + "\n")
	}
	var sealed bytes.Buffer
	if _, err := SealNDJSON(&sealed, &buf); err != nil {
		b.Fatal(err)
	}
	s, err := openNDJSONImage(sealed.Bytes())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.Close() })
	return s
}

func BenchmarkNDJSONCountAll(b *testing.B) {
	s := benchmarkQueryStore(b, 100000)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := s.Count(nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNDJSONCountWhere(b *testing.B) {
	s := benchmarkQueryStore(b, 100000)
	w, _ := ParseWhere(`status == "error"`)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := s.Count(w); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNDJSONAggregate(b *testing.B) {
	s := benchmarkQueryStore(b, 100000)
	w, _ := ParseWhere(`status == "error"`)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := s.Aggregate(w, "lat"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNDJSONGroupAggregate(b *testing.B) {
	s := benchmarkQueryStore(b, 100000)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := s.GroupAggregate(nil, "status", "lat"); err != nil {
			b.Fatal(err)
		}
	}
}
