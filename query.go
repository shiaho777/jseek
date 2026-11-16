package jseek

import (
	"strconv"
	"strings"
)

// Wildcard and filter queries — a gjson-compatible surface that lets callers
// harvest shapes, not just fixed paths:
//
//	jseek.Query(data, "users.#.name")             // every element's name
//	jseek.Query(data, "users.#(active==true)#.email") // all matches
//	jseek.QueryFirst(data, "users.#(age>30).name")    // first match
//	jseek.Query(data, "users.#")                  // array length
//
// Syntax (gjson-compatible where gjson defines it):
//   - "#" in a non-terminal position matches every element of an array
//     (gjson's array wildcard). Terminal "users.#" is the array length.
//   - "#(key op value)" selects array elements whose member satisfies the
//     comparison; ops: == != > >= < <=; values: JSON strings, numbers,
//     true/false/null; a bare key is an existence test. By default only the
//     FIRST matching element continues down the path (gjson semantics);
//     append "#" — "#(...)#" — to continue with ALL matches.
//   - "*" is a jseek extension: like "#" but also iterates object members.
//     (gjson's "*" is a key glob; jseek does not implement key globs.)
//   - Everything else behaves like GetPath (keys, [N] indices, bare numeric
//     segments).
//
// Results are collected into a caller-visible slice (one allocation per
// Query call); each returned value aliases data with Get semantics. The
// traversal itself never allocates and reuses the lazy skip primitives.

// Query extracts every value matching a wildcard/filter path. Missing paths
// yield an empty result, not an error; malformed input in the traversed
// region yields ErrMalformedJSON.
func Query(data []byte, path string) ([][]byte, error) {
	segs := parseQueryPath(path)
	var out [][]byte
	var stop bool
	err := queryWalk(data, skipWhitespace(data, 0), segs, &out, false, &stop)
	return out, err
}

// QueryResult is one typed match from QueryTyped. Raw aliases the input with
// Get semantics (strings exclude quotes); Type is the value's JSON type.
type QueryResult struct {
	Raw  []byte
	Type ValueType
}

// QueryTyped is Query with each result's ValueType preserved, so callers can
// distinguish a string "42" from the number 42 without re-parsing.
func QueryTyped(data []byte, path string) ([]QueryResult, error) {
	segs := parseQueryPath(path)
	var out []QueryResult
	var stop bool
	err := queryWalkTyped(data, skipWhitespace(data, 0), segs, &out, false, &stop)
	return out, err
}

func queryWalkTyped(data []byte, i int, segs []querySeg, out *[]QueryResult, first bool, stop *bool) error {
	if *stop {
		return nil
	}
	if len(segs) == 0 {
		v, vt, _, err := valueAt(data, i)
		if err != nil {
			return err
		}
		*out = append(*out, QueryResult{Raw: v, Type: vt})
		if first {
			*stop = true
		}
		return nil
	}
	seg := segs[0]
	rest := segs[1:]
	switch seg.kind {
	case qCount:
		if len(rest) > 0 {
			return ErrMalformedJSON
		}
		c, err := countAt(data, i)
		if err != nil {
			return err
		}
		*out = append(*out, QueryResult{Raw: strconv.AppendInt(nil, int64(c), 10), Type: Number})
		return nil
	case qKey:
		if i >= len(data) || seg.key == "" {
			return nil
		}
		if seg.key[0] == '[' {
			if data[i] != '[' {
				return nil
			}
			idx, ok := parseArrayIndex(seg.key)
			if !ok {
				return nil
			}
			j, found := findIndexGeneric(data, i, idx)
			if !found {
				return nil
			}
			return queryWalkTyped(data, j, rest, out, first, stop)
		}
		if data[i] != '{' {
			return nil
		}
		j, found := findKey(data, i, seg.key)
		if !found {
			return nil
		}
		return queryWalkTyped(data, j, rest, out, first, stop)
	case qWild:
		return queryEachChild(data, i, func(j int) error {
			return queryWalkTyped(data, j, rest, out, first, stop)
		}, stop)
	case qFilter:
		if i >= len(data) || data[i] != '[' {
			return nil
		}
		matchStop := !seg.filt.all
		return queryEachChild(data, i, func(j int) error {
			if !filterMatches(data, j, seg.filt) {
				return nil
			}
			if err := queryWalkTyped(data, j, rest, out, first, stop); err != nil {
				return err
			}
			if matchStop {
				*stop = true
			}
			return nil
		}, stop)
	}
	return nil
}

// QueryFirst returns the first value matching a wildcard/filter path, or
// ErrKeyPathNotFound if nothing matches. It stops traversal at the first hit.
func QueryFirst(data []byte, path string) ([]byte, ValueType, error) {
	segs := parseQueryPath(path)
	var res []byte
	var vt ValueType = NotExist
	found := false
	err := queryWalkFirst(data, skipWhitespace(data, 0), segs, &res, &vt, &found)
	if err != nil {
		return nil, NotExist, err
	}
	if !found {
		return nil, NotExist, ErrKeyPathNotFound
	}
	return res, vt, nil
}

// queryWalkFirst is queryWalk specialized to capture the first hit with its
// exact ValueType (valueAt semantics), stopping immediately afterwards.
func queryWalkFirst(data []byte, i int, segs []querySeg, res *[]byte, vt *ValueType, found *bool) error {
	if *found {
		return nil
	}
	if len(segs) == 0 {
		v, t, _, err := valueAt(data, i)
		if err != nil {
			return err
		}
		*res, *vt, *found = v, t, true
		return nil
	}
	seg := segs[0]
	rest := segs[1:]
	switch seg.kind {
	case qCount:
		if len(rest) > 0 {
			return ErrMalformedJSON
		}
		c, err := countAt(data, i)
		if err != nil {
			return err
		}
		*res, *vt, *found = strconv.AppendInt(nil, int64(c), 10), Number, true
		return nil
	case qKey:
		if i >= len(data) || seg.key == "" {
			return nil
		}
		if seg.key[0] == '[' {
			if data[i] != '[' {
				return nil
			}
			idx, ok := parseArrayIndex(seg.key)
			if !ok {
				return nil
			}
			j, ok2 := findIndexGeneric(data, i, idx)
			if !ok2 {
				return nil
			}
			return queryWalkFirst(data, j, rest, res, vt, found)
		}
		if data[i] != '{' {
			return nil
		}
		j, ok := findKey(data, i, seg.key)
		if !ok {
			return nil
		}
		return queryWalkFirst(data, j, rest, res, vt, found)
	case qWild:
		return queryEachChild(data, i, func(j int) error {
			return queryWalkFirst(data, j, rest, res, vt, found)
		}, found)
	case qFilter:
		if i >= len(data) || data[i] != '[' {
			return nil
		}
		return queryEachChild(data, i, func(j int) error {
			if !filterMatches(data, j, seg.filt) {
				return nil
			}
			return queryWalkFirst(data, j, rest, res, vt, found)
		}, found)
	}
	return nil
}

// Query on an indexed Document: navigation of literal prefixes could reuse
// the index, but the wildcard walk itself is byte-based; semantics match the
// package-level Query exactly.
func (d *Document) Query(path string) ([][]byte, error) {
	return Query(d.data, path)
}

// querySeg is one compiled query segment.
type querySeg struct {
	kind  queryKind
	key   string // literal key or "[N]"
	filt  queryFilter
	count bool // trailing "#": emit cardinality instead of values
}

type queryKind uint8

const (
	qKey queryKind = iota
	qWild
	qFilter
	qCount
)

type queryFilter struct {
	key    string
	op     string // "" means existence test
	val    []byte // raw JSON value for comparison
	hasVal bool
	all    bool // "#(...)#": continue with all matches, not just the first
}

// parseQueryPath compiles the query surface syntax into segments. It mirrors
// ParseDotPath for the literal subset (escapes, bare numerics, brackets).
func parseQueryPath(path string) []querySeg {
	if path == "" {
		return nil
	}
	var out []querySeg
	var cur strings.Builder
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		s := cur.String()
		cur.Reset()
		if s == "*" {
			out = append(out, querySeg{kind: qWild})
			return
		}
		if isAllDigits(s) {
			s = "[" + s + "]"
		}
		out = append(out, querySeg{kind: qKey, key: s})
	}
	for i := 0; i < len(path); i++ {
		c := path[i]
		switch c {
		case '\\':
			if i+1 < len(path) {
				cur.WriteByte(path[i+1])
				i++
			}
		case '.':
			flush()
		case '[':
			flush()
			j := strings.IndexByte(path[i:], ']')
			if j < 0 {
				cur.WriteByte(c)
				continue
			}
			out = append(out, querySeg{kind: qKey, key: path[i : i+j+1]})
			i += j
		case '#':
			flush()
			if i+1 < len(path) && path[i+1] == '(' {
				// Find the matching close paren (filters contain no nested
				// parens; string values may, so track quotes).
				j := i + 2
				inStr := false
				for j < len(path) {
					if path[j] == '"' && (j == i+2 || path[j-1] != '\\') {
						inStr = !inStr
					}
					if path[j] == ')' && !inStr {
						break
					}
					j++
				}
				if j < len(path) {
					f, ok := parseFilter(path[i+2 : j])
					if ok {
						if j+1 < len(path) && path[j+1] == '#' {
							f.all = true
							j++
						}
						out = append(out, querySeg{kind: qFilter, filt: f})
						i = j
						continue
					}
				}
				// Unparseable: treat '#' literally.
				cur.WriteByte('#')
				continue
			}
			// Terminal '#' is a count; non-terminal '#' is the array
			// wildcard (gjson's "users.#.name").
			if i == len(path)-1 {
				out = append(out, querySeg{kind: qCount})
			} else {
				out = append(out, querySeg{kind: qWild})
			}
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}

// parseFilter parses "key op value" (or bare "key") inside #(...).
func parseFilter(s string) (queryFilter, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return queryFilter{}, false
	}
	// Locate the operator outside of a leading quoted key (keys are plain).
	ops := []string{"==", "!=", ">=", "<=", ">", "<"}
	for _, op := range ops {
		if j := strings.Index(s, op); j > 0 {
			key := strings.TrimSpace(s[:j])
			val := strings.TrimSpace(s[j+len(op):])
			if key == "" || val == "" {
				return queryFilter{}, false
			}
			return queryFilter{key: key, op: op, val: []byte(val), hasVal: true}, true
		}
	}
	return queryFilter{key: s}, true
}

// queryWalk recursively resolves segments from the value at data[i]. In
// first-hit mode it sets *stop after emitting one value; in collect mode
// *stop stays false and every match is appended.
func queryWalk(data []byte, i int, segs []querySeg, out *[][]byte, first bool, stop *bool) error {
	if *stop {
		return nil
	}
	if len(segs) == 0 {
		v, _, _, err := valueAt(data, i)
		if err != nil {
			return err
		}
		*out = append(*out, v)
		if first {
			*stop = true
		}
		return nil
	}
	seg := segs[0]
	rest := segs[1:]
	switch seg.kind {
	case qCount:
		if len(rest) > 0 {
			return ErrMalformedJSON // count must be terminal
		}
		c, err := countAt(data, i)
		if err != nil {
			return err
		}
		*out = append(*out, strconv.AppendInt(nil, int64(c), 10))
		return nil
	case qKey:
		if i >= len(data) {
			return nil
		}
		if seg.key == "" {
			return nil
		}
		if seg.key[0] == '[' {
			if data[i] != '[' {
				return nil
			}
			idx, ok := parseArrayIndex(seg.key)
			if !ok {
				return nil
			}
			j, found := findIndexGeneric(data, i, idx)
			if !found {
				return nil
			}
			return queryWalk(data, j, rest, out, first, stop)
		}
		if data[i] != '{' {
			return nil
		}
		j, found := findKey(data, i, seg.key)
		if !found {
			return nil
		}
		return queryWalk(data, j, rest, out, first, stop)
	case qWild:
		return queryEachChild(data, i, func(j int) error {
			return queryWalk(data, j, rest, out, first, stop)
		}, stop)
	case qFilter:
		if i >= len(data) || data[i] != '[' {
			return nil
		}
		// Default: only the first matching element continues (gjson).
		// "#(...)#": every match continues.
		matchStop := !seg.filt.all
		return queryEachChild(data, i, func(j int) error {
			if !filterMatches(data, j, seg.filt) {
				return nil
			}
			if err := queryWalk(data, j, rest, out, first, stop); err != nil {
				return err
			}
			if matchStop {
				*stop = true
			}
			return nil
		}, stop)
	}
	return nil
}

// queryEachChild visits every element (array) or member value (object) of the
// container at data[i], calling fn with the offset of the value's first byte
// (including the opening quote for strings — unlike ArrayEach/ObjectEach,
// whose callback offset points at string content). Iteration stops early on
// error or when a first-hit stop has been satisfied.
func queryEachChild(data []byte, i int, fn func(int) error, stop *bool) error {
	n := len(data)
	if i >= n {
		return nil
	}
	var closer byte
	switch data[i] {
	case '[':
		closer = ']'
	case '{':
		closer = '}'
	default:
		return nil
	}
	j := skipWhitespace(data, i+1)
	if j < n && data[j] == closer {
		return nil
	}
	for j < n {
		if closer == '}' {
			// Object member: skip the key and colon first.
			if data[j] != '"' {
				return ErrMalformedJSON
			}
			ke, ok := skipString(data, j)
			if !ok {
				return ErrMalformedJSON
			}
			j = skipWhitespace(data, ke)
			if j >= n || data[j] != ':' {
				return ErrMalformedJSON
			}
			j = skipWhitespace(data, j+1)
		}
		if err := fn(j); err != nil {
			return err
		}
		if *stop {
			return nil
		}
		ve, ok := skipValue(data, j)
		if !ok {
			return ErrMalformedJSON
		}
		j = skipWhitespace(data, ve)
		if j >= n {
			return ErrMalformedJSON
		}
		switch data[j] {
		case ',':
			j = skipWhitespace(data, j+1)
		case closer:
			return nil
		default:
			return ErrMalformedJSON
		}
	}
	return ErrMalformedJSON
}

// filterMatches reports whether the object element at data[i] satisfies f.
func filterMatches(data []byte, i int, f queryFilter) bool {
	if i >= len(data) || data[i] != '{' {
		return false
	}
	j, found := findKey(data, i, f.key)
	if !found {
		return false
	}
	if !f.hasVal {
		return true // existence test
	}
	v, vt, _, err := valueAt(data, j)
	if err != nil {
		return false
	}
	return compareJSON(v, vt, f.val, f.op)
}

// compareJSON compares an extracted value against a raw JSON literal from the
// filter. Numbers compare numerically, strings lexicographically (decoded),
// booleans/null by equality. Type mismatches never satisfy ordering ops.
func compareJSON(v []byte, vt ValueType, want []byte, op string) bool {
	wt := classify(want, 0)
	switch {
	case vt == Number && wt == Number:
		a, err1 := parseFloat(v)
		b, err2 := parseFloat(want)
		if err1 != nil || err2 != nil {
			return false
		}
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
	case vt == String && wt == String:
		// want includes quotes; strip and unescape both sides.
		if len(want) < 2 {
			return false
		}
		a := string(unescapeInto(nil, v))
		b := string(unescapeInto(nil, want[1:len(want)-1]))
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
	case vt == Boolean && wt == Boolean:
		eq := string(v) == string(want)
		if op == "==" {
			return eq
		}
		if op == "!=" {
			return !eq
		}
	case vt == Null && wt == Null:
		if op == "==" {
			return true
		}
		if op == "!=" {
			return false
		}
	default:
		// Cross-type: only != is meaningful (and true).
		if op == "!=" {
			return true
		}
	}
	return false
}