package jseek

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"testing"
)

// aggEqual compares aggregates treating NaN as equal to NaN (Min/Max are NaN
// when no numeric values were seen).
func aggEqual(a, b Aggregate) bool {
	feq := func(x, y float64) bool { return x == y || (math.IsNaN(x) && math.IsNaN(y)) }
	return a.Count == b.Count && a.N == b.N && feq(a.Sum, b.Sum) && feq(a.Min, b.Min) && feq(a.Max, b.Max)
}

func buildIndexedNDJSON(t *testing.T, records []string, fields []string) *NDJSONStore {
	t.Helper()
	var buf bytes.Buffer
	n, err := SealNDJSONIndexed(&buf, bytes.NewBufferString(joinLines(records)), fields)
	if err != nil {
		t.Fatalf("seal indexed: %v", err)
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

// TestFieldIndexMatchesScan is the core correctness guarantee: an indexed query
// must return exactly what a full scan returns, for every operator and type.
func TestFieldIndexMatchesScan(t *testing.T) {
	var records []string
	for i := 0; i < 300; i++ {
		lat := (i * 37) % 500
		st := "ok"
		if i%3 == 0 {
			st = "error"
		}
		region := "us"
		if i%2 == 0 {
			region = "eu"
		}
		records = append(records, `{"id":`+strconv.Itoa(i)+`,"lat":`+strconv.Itoa(lat)+`,"status":"`+st+`","region":"`+region+`"}`)
	}
	// Some records missing fields, to test absent-field handling.
	records = append(records, `{"id":999}`, `{"id":1000,"lat":50}`, `{"id":1001,"status":"error"}`)

	indexed := buildIndexedNDJSON(t, records, []string{"lat", "status", "region"})
	scan := buildNDJSON(t, records) // no index

	wheres := []string{
		``,
		`lat > 100`,
		`lat >= 100`,
		`lat < 100`,
		`lat <= 100`,
		`lat == 100`,
		`lat != 100`,
		`status == "error"`,
		`status != "error"`,
		`region == "us"`,
		`lat > 100 && status == "error"`,
		`lat < 400 && region == "eu"`,
		`status == "error" && region == "us" && lat >= 50`,
		`lat == 99999`, // matches nothing
	}
	for _, ws := range wheres {
		w, err := ParseWhere(ws)
		if err != nil {
			t.Fatalf("parse %q: %v", ws, err)
		}
		gotCount, err := indexed.Count(w)
		if err != nil {
			t.Fatal(err)
		}
		wantCount, err := scan.Count(w)
		if err != nil {
			t.Fatal(err)
		}
		if gotCount != wantCount {
			t.Errorf("where %q: indexed count=%d scan count=%d", ws, gotCount, wantCount)
		}
		gotAgg, _ := indexed.Aggregate(w, "lat")
		wantAgg, _ := scan.Aggregate(w, "lat")
		if !aggEqual(gotAgg, wantAgg) {
			t.Errorf("where %q: indexed agg=%+v scan agg=%+v", ws, gotAgg, wantAgg)
		}
		gotGroup, _ := indexed.GroupAggregate(w, "region", "lat")
		wantGroup, _ := scan.GroupAggregate(w, "region", "lat")
		if len(gotGroup) != len(wantGroup) {
			t.Errorf("where %q: group count mismatch", ws)
		}
		for k, v := range wantGroup {
			if !aggEqual(gotGroup[k], v) {
				t.Errorf("where %q group %q: indexed=%+v scan=%+v", ws, k, gotGroup[k], v)
			}
		}
	}
}

// TestFieldIndexSkipsRecords proves the index actually reduces work: a selective
// query visits far fewer records than the total.
func TestFieldIndexSkipsRecords(t *testing.T) {
	var records []string
	for i := 0; i < 10000; i++ {
		st := "ok"
		if i == 5000 { // exactly one error record
			st = "error"
		}
		records = append(records, `{"id":`+strconv.Itoa(i)+`,"status":"`+st+`"}`)
	}
	s := buildIndexedNDJSON(t, records, []string{"status"})
	w, _ := ParseWhere(`status == "error"`)
	cands := s.candidates(w)
	if len(cands) != 1 || cands[0] != 5000 {
		t.Fatalf("candidates=%v want [5000]", cands)
	}
	n, err := s.Count(w)
	if err != nil || n != 1 {
		t.Errorf("count=%d err=%v want 1", n, err)
	}
}

// TestFieldIndexAbsentField confirms records missing the indexed field are not
// returned by equality but the scan agreement still holds.
func TestFieldIndexAbsentField(t *testing.T) {
	records := []string{`{"a":1}`, `{"b":2}`, `{"a":1,"b":3}`}
	s := buildIndexedNDJSON(t, records, []string{"a"})
	w, _ := ParseWhere(`a == 1`)
	n, _ := s.Count(w)
	if n != 2 {
		t.Errorf("count=%d want 2", n)
	}
}

// TestFieldIndexAppendPreserved checks AppendNDJSON keeps the index correct.
func TestFieldIndexAppendPreserved(t *testing.T) {
	var buf bytes.Buffer
	_, err := SealNDJSONIndexed(&buf, bytes.NewBufferString("{\"v\":1}\n{\"v\":2}\n"), []string{"v"})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	total, err := AppendNDJSON(&out, buf.Bytes(), bytes.NewBufferString("{\"v\":3}\n{\"v\":4}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 {
		t.Fatalf("total=%d want 4", total)
	}
	s, err := openNDJSONImage(out.Bytes())
	if err != nil {
		t.Fatalf("open appended: %v", err)
	}
	defer s.Close()
	if len(s.fields) != 1 {
		t.Fatalf("fields=%d want 1 (index preserved)", len(s.fields))
	}
	w, _ := ParseWhere(`v > 2`)
	n, _ := s.Count(w)
	if n != 2 {
		t.Errorf("count=%d want 2", n)
	}
	// Cross-check against scan.
	scan := buildNDJSON(t, []string{`{"v":1}`, `{"v":2}`, `{"v":3}`, `{"v":4}`})
	sn, _ := scan.Count(w)
	if n != sn {
		t.Errorf("indexed=%d scan=%d", n, sn)
	}
}

// FuzzFieldIndexMatchesScan differential-fuzzes indexed vs scan Count over
// generated records and thresholds.
func FuzzFieldIndexMatchesScan(f *testing.F) {
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
		if len(records) == 0 || len(records) > 150 {
			return
		}
		indexed := buildIndexedNDJSON(t, records, []string{"v"})
		scan := buildNDJSON(t, records)
		for _, op := range []string{">", ">=", "<", "<=", "==", "!="} {
			w, err := ParseWhere(`v ` + op + ` ` + strconv.Itoa(threshold))
			if err != nil {
				continue
			}
			got, _ := indexed.Count(w)
			want, _ := scan.Count(w)
			if got != want {
				t.Fatalf("op %s threshold %d: indexed=%d scan=%d", op, threshold, got, want)
			}
		}
	})
}

func BenchmarkFieldIndexSelective(b *testing.B) {
	var buf bytes.Buffer
	for i := 0; i < 100000; i++ {
		st := "ok"
		if i%100 == 0 { // 1% selectivity
			st = "error"
		}
		buf.WriteString(`{"id":` + strconv.Itoa(i) + `,"status":"` + st + `"}` + "\n")
	}
	var sealed bytes.Buffer
	if _, err := SealNDJSONIndexed(&sealed, &buf, []string{"status"}); err != nil {
		b.Fatal(err)
	}
	s, err := openNDJSONImage(sealed.Bytes())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.Close() })
	w, _ := ParseWhere(`status == "error"`)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := s.Count(w); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFieldIndexScanFallback(b *testing.B) {
	var buf bytes.Buffer
	for i := 0; i < 100000; i++ {
		st := "ok"
		if i%100 == 0 {
			st = "error"
		}
		buf.WriteString(`{"id":` + strconv.Itoa(i) + `,"status":"` + st + `"}` + "\n")
	}
	var sealed bytes.Buffer
	if _, err := SealNDJSON(&sealed, &buf); err != nil { // no index
		b.Fatal(err)
	}
	s, err := openNDJSONImage(sealed.Bytes())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.Close() })
	w, _ := ParseWhere(`status == "error"`)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := s.Count(w); err != nil {
			b.Fatal(err)
		}
	}
}
