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
//   - "..key" is recursive descent (jq-style): finds every member named key
//     at any depth, e.g. "..name" or "users..address.city".
//   - "[start:end]" slices an array (jq/Python-style); bounds are optional
//     and negatives count from the end: "[1:3]", "[:2]", "[-2:]".
//   - Filters combine with "&&" / "||" (&& binds tighter):
//     "#(age>30 && active==true)#".
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
	case qRecursive:
		return queryRecursive(data, i, seg.key, func(j int) error {
			return queryWalkTyped(data, j, rest, out, first, stop)
		}, stop)
	case qSlice:
		return querySliceWalk(data, i, seg.slice, func(j int) error {
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
	case qRecursive:
		return queryRecursive(data, i, seg.key, func(j int) error {
			return queryWalkFirst(data, j, rest, res, vt, found)
		}, found)
	case qSlice:
		return querySliceWalk(data, i, seg.slice, func(j int) error {
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
	slice querySlice
	count bool // trailing "#": emit cardinality instead of values
}

type queryKind uint8

const (
	qKey queryKind = iota
	qWild
	qFilter
	qCount
	qRecursive // "..key": find key at any depth
	qSlice     // "[start:end]": array slice
)

// querySlice is a Python/jq-style array slice. Bounds are optional; negative
// bounds count from the end. hasStart/hasEnd distinguish "[ :3]" from "[0:3]".
type querySlice struct {
	start, end       int
	hasStart, hasEnd bool
}

type queryFilter struct {
	key    string
	op     string // "" means existence test
	val    []byte // raw JSON value for comparison
	hasVal bool
	all    bool // "#(...)#": continue with all matches, not just the first
	// Compound form: when logic != "", left/right are combined instead of
	// the single key/op/val test.
	logic string // "&&" or "||"
	left  *queryFilter
	right *queryFilter
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
			// ".." introduces recursive descent: the following key is
			// searched at every depth ("..name", "users..name").
			if i+1 < len(path) && path[i+1] == '.' {
				i++
				j := i + 1
				for j < len(path) && path[j] != '.' && path[j] != '[' {
					if path[j] == '\\' && j+1 < len(path) {
						j++
					}
					j++
				}
				key := unescapeKey(path[i+1 : j])
				out = append(out, querySeg{kind: qRecursive, key: key})
				i = j - 1
			}
		case '[':
			flush()
			j := strings.IndexByte(path[i:], ']')
			if j < 0 {
				cur.WriteByte(c)
				continue
			}
			content := path[i+1 : i+j]
			if sl, ok := parseSlice(content); ok {
				out = append(out, querySeg{kind: qSlice, slice: sl})
			} else {
				out = append(out, querySeg{kind: qKey, key: path[i : i+j+1]})
			}
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

// parseFilter parses a filter expression inside #(...): a single
// "key op value" (or bare "key" existence test), or two expressions joined
// by "&&" / "||" (left-associative, && binds tighter than ||).
func parseFilter(s string) (queryFilter, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return queryFilter{}, false
	}
	return parseFilterOr(s)
}

// parseFilterOr splits on the LAST top-level "||" (left-associative) and
// recurses; each side below an || is an &&-term.
func parseFilterOr(s string) (queryFilter, bool) {
	if j := lastLogic(s, "||"); j >= 0 {
		l, ok1 := parseFilterOr(s[:j])
		r, ok2 := parseFilterAnd(s[j+2:])
		if !ok1 || !ok2 {
			return queryFilter{}, false
		}
		return queryFilter{logic: "||", left: &l, right: &r}, true
	}
	return parseFilterAnd(s)
}

// parseFilterAnd splits on the LAST top-level "&&" and recurses; operands
// are atoms. Splitting at the last operator yields left-associative grouping.
func parseFilterAnd(s string) (queryFilter, bool) {
	if j := lastLogic(s, "&&"); j >= 0 {
		l, ok1 := parseFilterAnd(s[:j])
		r, ok2 := parseFilterAtom(s[j+2:])
		if !ok1 || !ok2 {
			return queryFilter{}, false
		}
		return queryFilter{logic: "&&", left: &l, right: &r}, true
	}
	return parseFilterAtom(s)
}

// lastLogic returns the index of the last top-level occurrence of sep
// (outside quoted strings), or -1.
func lastLogic(s, sep string) int {
	last := -1
	inStr := false
	for i := 0; i+len(sep) <= len(s); i++ {
		c := s[i]
		if c == '"' && (i == 0 || s[i-1] != '\\') {
			inStr = !inStr
			continue
		}
		if !inStr && strings.HasPrefix(s[i:], sep) {
			last = i
			i++ // skip the second byte of the separator
		}
	}
	return last
}

func parseFilterAtom(s string) (queryFilter, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return queryFilter{}, false
	}
	// Scan for a comparison operator outside quoted strings, skipping the
	// '&&' logical connector (its '&' must not be read as part of an op).
	inStr := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' && (i == 0 || s[i-1] != '\\') {
			inStr = !inStr
			continue
		}
		if inStr {
			continue
		}
		if c == '&' && i+1 < len(s) && s[i+1] == '&' {
			i++
			continue
		}
		var op string
		switch {
		case strings.HasPrefix(s[i:], "=="):
			op = "=="
		case strings.HasPrefix(s[i:], "!="):
			op = "!="
		case strings.HasPrefix(s[i:], ">="):
			op = ">="
		case strings.HasPrefix(s[i:], "<="):
			op = "<="
		case c == '>':
			op = ">"
		case c == '<':
			op = "<"
		}
		if op != "" && i > 0 {
			key := strings.TrimSpace(s[:i])
			val := strings.TrimSpace(s[i+len(op):])
			if key == "" || val == "" {
				return queryFilter{}, false
			}
			return queryFilter{key: key, op: op, val: []byte(val), hasVal: true}, true
		}
	}
	return queryFilter{key: s}, true
}

// parseSlice parses "start:end" slice content (without brackets). Both bounds
// are optional; negatives count from the end. Returns false for non-slice
// content (e.g. a plain index "3").
func parseSlice(s string) (querySlice, bool) {
	colon := strings.IndexByte(s, ':')
	if colon < 0 {
		return querySlice{}, false
	}
	var sl querySlice
	startStr := strings.TrimSpace(s[:colon])
	endStr := strings.TrimSpace(s[colon+1:])
	if startStr != "" {
		n, ok := parseSignedInt(startStr)
		if !ok {
			return querySlice{}, false
		}
		sl.start, sl.hasStart = n, true
	}
	if endStr != "" {
		n, ok := parseSignedInt(endStr)
		if !ok {
			return querySlice{}, false
		}
		sl.end, sl.hasEnd = n, true
	}
	return sl, true
}

func parseSignedInt(s string) (int, bool) {
	neg := false
	if s != "" && (s[0] == '-' || s[0] == '+') {
		neg = s[0] == '-'
		s = s[1:]
	}
	if s == "" || !isAllDigits(s) {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		n = n*10 + int(s[i]-'0')
		if n > 1<<30 {
			return 0, false
		}
	}
	if neg {
		n = -n
	}
	return n, true
}

// unescapeKey reverses backslash escapes in a dotted-path key segment.
func unescapeKey(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
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
	case qRecursive:
		return queryRecursive(data, i, seg.key, func(j int) error {
			return queryWalk(data, j, rest, out, first, stop)
		}, stop)
	case qSlice:
		return querySliceWalk(data, i, seg.slice, func(j int) error {
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
	if f.logic != "" {
		if f.logic == "&&" {
			return filterMatches(data, i, *f.left) && filterMatches(data, i, *f.right)
		}
		return filterMatches(data, i, *f.left) || filterMatches(data, i, *f.right)
	}
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

// queryRecursive visits every object member value whose key equals key, at
// any nesting depth below data[i] (including i itself). It walks containers
// with the lazy skip primitives and never decodes values it does not match.
func queryRecursive(data []byte, i int, key string, fn func(int) error, stop *bool) error {
	if *stop || i >= len(data) {
		return nil
	}
	switch data[i] {
	case '{':
		return queryEachChildKV(data, i, func(k []byte, v int) error {
			if string(k) == key {
				if err := fn(v); err != nil {
					return err
				}
				if *stop {
					return nil
				}
			}
			return queryRecursive(data, v, key, fn, stop)
		}, stop)
	case '[':
		return queryEachChild(data, i, func(v int) error {
			return queryRecursive(data, v, key, fn, stop)
		}, stop)
	default:
		return nil
	}
}

// queryEachChildKV is queryEachChild for objects, additionally passing each
// member's decoded key. Arrays are iterated as values with nil keys.
func queryEachChildKV(data []byte, i int, fn func(key []byte, v int) error, stop *bool) error {
	n := len(data)
	if i >= n || data[i] != '{' {
		return nil
	}
	j := skipWhitespace(data, i+1)
	if j < n && data[j] == '}' {
		return nil
	}
	for j < n {
		if data[j] != '"' {
			return ErrMalformedJSON
		}
		ke, ok := skipString(data, j)
		if !ok {
			return ErrMalformedJSON
		}
		key := data[j+1 : ke-1]
		if strings.IndexByte(string(key), '\\') >= 0 {
			key = unescapeInto(nil, key)
		}
		j = skipWhitespace(data, ke)
		if j >= n || data[j] != ':' {
			return ErrMalformedJSON
		}
		j = skipWhitespace(data, j+1)
		if err := fn(key, j); err != nil {
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
		case '}':
			return nil
		default:
			return ErrMalformedJSON
		}
	}
	return ErrMalformedJSON
}

// querySliceWalk iterates the array elements selected by sl, calling fn with
// each element's value offset. Bounds are clamped to the array length;
// negative bounds count from the end.
func querySliceWalk(data []byte, i int, sl querySlice, fn func(int) error, stop *bool) error {
	if i >= len(data) || data[i] != '[' {
		return nil
	}
	// Count elements once to resolve negative/clamped bounds.
	count, err := countArray(data, i)
	if err != nil {
		return err
	}
	start, end := 0, count
	if sl.hasStart {
		start = sl.start
		if start < 0 {
			start += count
		}
	}
	if sl.hasEnd {
		end = sl.end
		if end < 0 {
			end += count
		}
	}
	if start < 0 {
		start = 0
	}
	if end > count {
		end = count
	}
	if start >= end {
		return nil
	}
	idx := 0
	localStop := false
	err = queryEachChild(data, i, func(j int) error {
		if idx >= start && idx < end {
			if err := fn(j); err != nil {
				return err
			}
		}
		idx++
		if idx >= end || *stop {
			localStop = true // no further elements can match
		}
		return nil
	}, &localStop)
	return err
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