package jseek

import (
	"bytes"
	"math"
	"sort"
	"strconv"
	"strings"
)

// NDJSON query engine — turns the random-access store into a real log-analytics
// engine. The offset table gives O(1) record access and each record carries a
// sealed structural index, so filtering, projection, and aggregation all run on
// the zero-parse substrate: no record is ever re-parsed, and a WHERE clause
// touches only the fields it tests. This is the "SQLite for JSON logs" query
// layer — SELECT fields FROM records WHERE cond GROUP BY key.
//
// Everything here is read-only and safe for concurrent use on an open store
// (the mapping is immutable). Iteration reuses one Document (zero alloc per
// record); only matched/projected output allocates.

// Predicate is one field comparison: <path> <op> <literal>. The literal is raw
// JSON (a quoted string, a number, true/false/null), so types are inferred the
// same way as the query engine's filters.
//
// The literal's type and (for strings) decoded value are precomputed once at
// parse time, and a reusable decode buffer is carried per predicate, so
// evaluating a WHERE over many records allocates nothing per record.
type Predicate struct {
	Path []string
	Op   string // == != > >= < <=
	Want []byte // raw JSON literal

	wantType ValueType
	wantStr  string // decoded literal (strings only), computed once
	scratch  []byte // reusable buffer for decoding the field value
}

// Where is a conjunction of predicates (AND). A record matches when every
// predicate holds. An empty Where matches every record.
type Where []Predicate

// ParseWhere parses a simple conjunction of comparisons, e.g.
//
//	latency_ms > 100 && status == "error" && region != "us"
//
// Supported operators: == != > >= < <=. Literals are JSON tokens (quoted
// strings, numbers, true/false/null). Paths are dotted key paths. This is a
// deliberately small, predictable subset — not a full expression language.
func ParseWhere(s string) (Where, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var w Where
	for _, part := range splitTopLevel(s, "&&") {
		part = strings.TrimSpace(part)
		p, err := parsePredicate(part)
		if err != nil {
			return nil, err
		}
		w = append(w, p)
	}
	return w, nil
}

func parsePredicate(s string) (Predicate, error) {
	// Find the operator (longest first so >= is not read as >).
	ops := []string{">=", "<=", "==", "!=", ">", "<"}
	for _, op := range ops {
		if i := strings.Index(s, op); i >= 0 {
			path := strings.TrimSpace(s[:i])
			lit := strings.TrimSpace(s[i+len(op):])
			if path == "" || lit == "" {
				return Predicate{}, ErrMalformedJSON
			}
			litBytes := []byte(lit)
			if !Valid(litBytes) {
				return Predicate{}, ErrMalformedJSON
			}
			p := Predicate{Path: ParseDotPath(path), Op: op, Want: litBytes}
			p.wantType = classify(litBytes, 0)
			if p.wantType == String && len(litBytes) >= 2 {
				p.wantStr = string(unescapeInto(nil, litBytes[1:len(litBytes)-1]))
			}
			return p, nil
		}
	}
	return Predicate{}, ErrMalformedJSON
}

// splitTopLevel splits on sep, ignoring separators inside quoted strings.
func splitTopLevel(s, sep string) []string {
	var out []string
	inStr := false
	esc := false
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if esc {
			esc = false
			continue
		}
		if c == '\\' && inStr {
			esc = true
			continue
		}
		if c == '"' {
			inStr = !inStr
			continue
		}
		if !inStr && strings.HasPrefix(s[i:], sep) {
			out = append(out, s[start:i])
			i += len(sep) - 1
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

// eval reports whether record d satisfies the predicate, using indexed lookup
// and the precomputed literal. It allocates nothing per record: string field
// values are decoded into the predicate's reusable scratch buffer.
func (p *Predicate) eval(d *Document) bool {
	v, vt, _, err := d.Get(p.Path...)
	if err != nil {
		return false // missing field never matches
	}
	// Fast paths that need no decoding.
	if vt == Number && p.wantType == Number {
		a, e1 := parseFloat(v)
		b, e2 := parseFloat(p.Want)
		if e1 != nil || e2 != nil {
			return false
		}
		return cmpOrdered(a, b, p.Op)
	}
	if vt == String && p.wantType == String {
		// Decode the field value into scratch (reused across records).
		p.scratch = unescapeInto(p.scratch[:0], v)
		return cmpStrings(string(p.scratch), p.wantStr, p.Op)
	}
	if vt == Boolean && p.wantType == Boolean {
		eq := string(v) == string(p.Want)
		return boolOp(eq, p.Op)
	}
	if vt == Null && p.wantType == Null {
		return p.Op == "=="
	}
	// Cross-type: only != is meaningful.
	return p.Op == "!="
}

func cmpOrdered(a, b float64, op string) bool {
	switch op {
	case "==":
		return a == b
	case "!=":
		return a != b
	case ">":
		return a > b
	case ">=":
		return a >= b
	case "<":
		return a < b
	case "<=":
		return a <= b
	}
	return false
}

func cmpStrings(a, b, op string) bool {
	switch op {
	case "==":
		return a == b
	case "!=":
		return a != b
	case ">":
		return a > b
	case ">=":
		return a >= b
	case "<":
		return a < b
	case "<=":
		return a <= b
	}
	return false
}

func boolOp(eq bool, op string) bool {
	if op == "==" {
		return eq
	}
	if op == "!=" {
		return !eq
	}
	return false
}

// match reports whether d satisfies every predicate.
func (w Where) match(d *Document) bool {
	for i := range w {
		if !w[i].eval(d) {
			return false
		}
	}
	return true
}

// candidates returns the sorted record IDs that may match w, using the field
// index when available. if nil, the caller must scan every record. When the
// index proves no record can match, it returns an empty (non-nil) slice.
//
// Strategy: resolve each indexed predicate to a record-ID set by binary search,
// then intersect the sets (smallest first). Unindexed predicates are left for
// per-record evaluation. If no predicate is indexed, fall back to a full scan.
func (s *NDJSONStore) candidates(w Where) []uint64 {
	if len(s.fields) == 0 || len(w) == 0 {
		return nil
	}
	var sets [][]uint64
	for i := range w {
		p := &w[i]
		f := s.lookupField(strings.Join(p.Path, "."))
		if f == nil {
			continue // unindexed predicate: evaluate per record
		}
		set, ok := f.candidateRange(p.Op, p.Want)
		if !ok {
			continue
		}
		sets = append(sets, set)
	}
	if len(sets) == 0 {
		return nil // nothing indexed: scan
	}
	// Intersect, smallest set first to minimize work.
	sort.Slice(sets, func(i, j int) bool { return len(sets[i]) < len(sets[j]) })
	cur := sets[0]
	for _, next := range sets[1:] {
		cur = intersectSorted(cur, next)
		if len(cur) == 0 {
			break
		}
	}
	if cur == nil {
		cur = []uint64{}
	}
	return cur
}

// intersectSorted returns the sorted intersection of two sorted uint64 slices.
func intersectSorted(a, b []uint64) []uint64 {
	out := make([]uint64, 0, min(len(a), len(b)))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, a[i])
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return out
}

// forEachMatch visits every record matching w, using the field index to skip
// non-candidates when possible. The Document is reused (zero alloc per record).
func (s *NDJSONStore) forEachMatch(w Where, fn func(i int, d *Document) bool) error {
	cands := s.candidates(w)
	var d Document
	if cands == nil {
		for i := 0; i < s.n; i++ {
			if _, err := s.RecordInto(&d, i); err != nil {
				return err
			}
			if w.match(&d) && !fn(i, &d) {
				return nil
			}
		}
		return nil
	}
	for _, rec := range cands {
		i := int(rec)
		if _, err := s.RecordInto(&d, i); err != nil {
			return err
		}
		if w.match(&d) && !fn(i, &d) {
			return nil
		}
	}
	return nil
}

// Select iterates records matching w, projecting the given dotted field paths
// (nil/empty projects the whole record). fn receives the record index and the
// projected raw JSON values in order; return false to stop early. Projection
// uses indexed extraction, so only the selected fields are touched.
func (s *NDJSONStore) Select(w Where, fields []string, fn func(i int, vals [][]byte, types []ValueType) bool) error {
	paths := make([][]string, len(fields))
	for i, f := range fields {
		paths[i] = ParseDotPath(f)
	}
	return s.forEachMatch(w, func(i int, d *Document) bool {
		if len(paths) == 0 {
			raw, _ := s.Raw(i)
			return fn(i, [][]byte{raw}, []ValueType{Object})
		}
		vals := make([][]byte, len(paths))
		types := make([]ValueType, len(paths))
		for j, p := range paths {
			v, vt, _, err := d.Get(p...)
			if err != nil {
				vals[j], types[j] = nil, NotExist
			} else {
				vals[j], types[j] = v, vt
			}
		}
		return fn(i, vals, types)
	})
}

// writeJSONValue renders an extracted value as a standalone JSON token. Strings
// from Get exclude quotes, so they are re-quoted here; other types are already
// valid JSON tokens.
func writeJSONValue(buf *bytes.Buffer, v []byte, vt ValueType) {
	if v == nil {
		buf.WriteString("null")
		return
	}
	if vt == String {
		buf.WriteByte('"')
		buf.Write(v)
		buf.WriteByte('"')
		return
	}
	buf.Write(v)
}

// Count returns the number of records matching w (nil counts all). It evaluates
// only the WHERE fields per record — no projection, no allocation.
func (s *NDJSONStore) Count(w Where) (int, error) {
	n := 0
	err := s.forEachMatch(w, func(i int, d *Document) bool {
		n++
		return true
	})
	return n, err
}

// Aggregate is the result of a numeric aggregation over matching records.
type Aggregate struct {
	Count int // records matched
	N     int // records where the field was a finite number
	Sum   float64
	Min   float64
	Max   float64
}

// Avg returns Sum/N, or NaN when no numeric values were seen.
func (a Aggregate) Avg() float64 {
	if a.N == 0 {
		return math.NaN()
	}
	return a.Sum / float64(a.N)
}

// Aggregate computes count/sum/min/max/avg of a numeric field over records
// matching w. Non-numeric or missing values are skipped (counted in Count but
// not N). Single pass, indexed extraction, zero per-record allocation.
func (s *NDJSONStore) Aggregate(w Where, field string) (Aggregate, error) {
	path := ParseDotPath(field)
	agg := Aggregate{Min: math.Inf(1), Max: math.Inf(-1)}
	err := s.forEachMatch(w, func(i int, d *Document) bool {
		agg.Count++
		v, vt, _, err := d.Get(path...)
		if err != nil || vt != Number {
			return true
		}
		f, err := parseFloat(v)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return true
		}
		agg.N++
		agg.Sum += f
		if f < agg.Min {
			agg.Min = f
		}
		if f > agg.Max {
			agg.Max = f
		}
		return true
	})
	if err != nil {
		return Aggregate{}, err
	}
	if agg.N == 0 {
		agg.Min, agg.Max = math.NaN(), math.NaN()
	}
	return agg, nil
}

// GroupAggregate aggregates a numeric field per distinct value of a group key,
// over records matching w. The group key is extracted as a string (numbers and
// booleans are rendered canonically); records missing the group key are skipped.
// Returns a map from group value to its Aggregate. Single pass.
func (s *NDJSONStore) GroupAggregate(w Where, groupField, aggField string) (map[string]Aggregate, error) {
	gpath := ParseDotPath(groupField)
	apath := ParseDotPath(aggField)
	out := make(map[string]Aggregate)
	intern := make(map[string]string) // canonical stable key per group
	var scratch []byte                // reusable decode buffer for string group keys
	err := s.forEachMatch(w, func(i int, d *Document) bool {
		gv, gvt, _, err := d.Get(gpath...)
		if err != nil {
			return true
		}
		// Render the group key into scratch (strings decoded, others raw). Both
		// the intern lookup and the aggregate lookup use string(scratch), which
		// the compiler keeps allocation-free; a stable key string is allocated
		// only once per distinct group.
		if gvt == String {
			scratch = unescapeInto(scratch[:0], gv)
		} else {
			scratch = append(scratch[:0], gv...)
		}
		key, seen := intern[string(scratch)]
		if !seen {
			key = string(append([]byte(nil), scratch...))
			intern[key] = key
		}
		agg, exists := out[key]
		if !exists {
			agg.Min, agg.Max = math.Inf(1), math.Inf(-1)
		}
		agg.Count++
		av, avt, _, err := d.Get(apath...)
		if err == nil && avt == Number {
			if f, e := parseFloat(av); e == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
				agg.N++
				agg.Sum += f
				if f < agg.Min {
					agg.Min = f
				}
				if f > agg.Max {
					agg.Max = f
				}
			}
		}
		out[key] = agg
		return true
	})
	if err != nil {
		return nil, err
	}
	for k, agg := range out {
		if agg.N == 0 {
			agg.Min, agg.Max = math.NaN(), math.NaN()
			out[k] = agg
		}
	}
	return out, nil
}

// SelectJSON is a convenience that writes matching records (projected) as NDJSON
// to a builder, for CLI use. fields empty emits whole records.
func (s *NDJSONStore) SelectJSON(w Where, fields []string, limit int) ([]byte, error) {
	var buf bytes.Buffer
	n := 0
	err := s.Select(w, fields, func(i int, vals [][]byte, types []ValueType) bool {
		if limit > 0 && n >= limit {
			return false
		}
		if len(fields) == 0 {
			buf.Write(vals[0])
		} else if len(fields) == 1 {
			writeJSONValue(&buf, vals[0], types[0])
		} else {
			buf.WriteByte('{')
			for j, f := range fields {
				if j > 0 {
					buf.WriteByte(',')
				}
				buf.WriteString(strconv.Quote(f))
				buf.WriteByte(':')
				writeJSONValue(&buf, vals[j], types[j])
			}
			buf.WriteByte('}')
		}
		buf.WriteByte('\n')
		n++
		return true
	})
	return buf.Bytes(), err
}
