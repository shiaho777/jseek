package jseek

import "strings"

// Path syntaxes. jseek's native path form is a []string of segments where array
// indices look like "[0]". These helpers let callers express paths as a single
// string in two popular notations, compiling them to the native form.

// ParsePointer parses an RFC 6901 JSON Pointer into jseek's native key path.
//
// A pointer is a string of "/"-prefixed reference tokens, e.g. "/a/b/0/c". The
// escape sequences ~1 (=> "/") and ~0 (=> "~") are decoded. A numeric token is
// converted to an array-index segment ("[N]") so it can address array elements;
// note this means a numeric token can also match an object key spelled as that
// number only via array semantics — JSON Pointer itself is ambiguous here and
// jseek resolves numeric tokens as array indices, falling back is the caller's
// responsibility.
//
// The empty pointer "" denotes the whole document and yields a nil path.
func ParsePointer(pointer string) ([]string, error) {
	if pointer == "" {
		return nil, nil
	}
	if pointer[0] != '/' {
		return nil, &PathError{Err: ErrMalformedJSON, At: -1, Offset: 0, Got: Unknown, Want: Unknown}
	}
	tokens := strings.Split(pointer[1:], "/")
	out := make([]string, len(tokens))
	for i, tok := range tokens {
		tok = decodePointerToken(tok)
		if isAllDigits(tok) {
			out[i] = "[" + tok + "]"
		} else {
			out[i] = tok
		}
	}
	return out, nil
}

// decodePointerToken decodes the ~1 and ~0 escapes of a JSON Pointer token.
func decodePointerToken(tok string) string {
	if !strings.Contains(tok, "~") {
		return tok
	}
	// ~1 -> /, ~0 -> ~  (order matters: replace ~1 first).
	tok = strings.ReplaceAll(tok, "~1", "/")
	tok = strings.ReplaceAll(tok, "~0", "~")
	return tok
}

// ParseDotPath parses a dotted path like "a.b[0].c" or "users.0.name" into
// jseek's native key path. Both bracket indices ("a[0]") and bare numeric
// segments interpreted as indices ("a.0") are supported. Dots inside a segment
// can be escaped with a backslash ("a\.b" is one key "a.b").
//
// A trailing ".#" (or a bare "#") is a count marker: GetPath/Get on such a
// path return the number of elements/members of the container at the prefix
// path as a Number value. "#" elsewhere in a path is an ordinary key byte.
func ParseDotPath(path string) []string {
	if path == "" {
		return nil
	}
	if path == "#" || strings.HasSuffix(path, ".#") {
		prefix := ParseDotPath(strings.TrimSuffix(path, "#"))
		if len(prefix) > 0 && prefix[len(prefix)-1] == "" {
			prefix = prefix[:len(prefix)-1]
		}
		return append(prefix, countSeg)
	}
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = appendDotSegment(out, cur.String())
			cur.Reset()
		}
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
			// Start of a bracket index; flush any accumulated key first.
			flush()
			j := strings.IndexByte(path[i:], ']')
			if j < 0 {
				cur.WriteByte(c)
				continue
			}
			out = append(out, path[i:i+j+1]) // includes [ and ]
			i += j
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}

// appendDotSegment appends seg, converting a bare numeric segment into an
// array-index segment.
func appendDotSegment(out []string, seg string) []string {
	if isAllDigits(seg) {
		return append(out, "["+seg+"]")
	}
	return append(out, seg)
}

func isAllDigits(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// maxStackSegs bounds the stack-resident segment buffer used by the zero-
// allocation path queries. Deeper or escaped paths fall back to the allocating
// parsers, which handle arbitrary depth.
const maxStackSegs = 16

// parseDotPathInto parses a dotted path into dst without allocating, provided
// the path needs no escape decoding, no bare-numeric index conversion, and at
// most cap(dst) segments. It returns the segments and true on success; on
// false the caller must fall back to ParseDotPath (dst contents are then
// undefined). Semantics match ParseDotPath exactly for the accepted subset.
func parseDotPathInto(dst []string, path string) ([]string, bool) {
	if path == "" {
		return dst[:0], true
	}
	count := false
	if path == "#" || strings.HasSuffix(path, ".#") {
		count = true
		path = strings.TrimSuffix(path, "#")
		path = strings.TrimSuffix(path, ".")
	}
	out := dst[:0]
	segStart := 0
	for i := 0; i < len(path); i++ {
		switch path[i] {
		case '\\':
			return nil, false // escape decoding needs a fresh string
		case '.':
			if i > segStart {
				if len(out) == cap(dst) || isAllDigits(path[segStart:i]) {
					return nil, false
				}
				out = append(out, path[segStart:i])
			}
			segStart = i + 1
		case '[':
			if i > segStart {
				if len(out) == cap(dst) || isAllDigits(path[segStart:i]) {
					return nil, false
				}
				out = append(out, path[segStart:i])
			}
			j := strings.IndexByte(path[i:], ']')
			if j < 0 {
				return nil, false // unclosed bracket: literal, needs builder
			}
			if len(out) == cap(dst) {
				return nil, false
			}
			out = append(out, path[i:i+j+1])
			i += j
			segStart = i + 1
		}
	}
	if len(path) > segStart {
		if len(out) == cap(dst) || isAllDigits(path[segStart:]) {
			return nil, false
		}
		out = append(out, path[segStart:])
	}
	if count {
		if len(out) == cap(dst) {
			return nil, false
		}
		out = append(out, countSeg)
	}
	return out, true
}

// parsePointerInto is the zero-allocation counterpart of ParsePointer for
// pointers with no ~ escapes and at most cap(dst) tokens. Numeric tokens need
// bracket wrapping and fall back to the allocating parser.
func parsePointerInto(dst []string, pointer string) ([]string, bool, error) {
	if pointer == "" {
		return dst[:0], true, nil
	}
	if pointer[0] != '/' {
		return nil, false, &PathError{Err: ErrMalformedJSON, At: -1, Offset: 0, Got: Unknown, Want: Unknown}
	}
	out := dst[:0]
	rest := pointer[1:]
	for {
		j := strings.IndexByte(rest, '/')
		tok := rest
		if j >= 0 {
			tok = rest[:j]
		}
		if strings.IndexByte(tok, '~') >= 0 || isAllDigits(tok) {
			return nil, false, nil
		}
		if len(out) == cap(dst) {
			return nil, false, nil
		}
		out = append(out, tok)
		if j < 0 {
			break
		}
		rest = rest[j+1:]
	}
	return out, true, nil
}

// GetPointer extracts the value addressed by an RFC 6901 JSON Pointer.
// Simple pointers (no ~ escapes, at most 16 tokens) are resolved without
// allocating.
func GetPointer(data []byte, pointer string) (value []byte, dataType ValueType, offset int, err error) {
	var buf [maxStackSegs]string
	if keys, ok, perr := parsePointerInto(buf[:0], pointer); perr != nil {
		return nil, NotExist, 0, perr
	} else if ok {
		return Get(data, keys...)
	}
	keys, perr := ParsePointer(pointer)
	if perr != nil {
		return nil, NotExist, 0, perr
	}
	return Get(data, keys...)
}

// GetPath extracts the value addressed by a dotted path like "a.b[0].c".
// Simple paths (no escapes or bare numeric segments, at most 16 segments) are
// resolved without allocating.
func GetPath(data []byte, path string) (value []byte, dataType ValueType, offset int, err error) {
	var buf [maxStackSegs]string
	if keys, ok := parseDotPathInto(buf[:0], path); ok {
		if n := len(keys); n > 0 && keys[n-1] == countSeg {
			return countPathResult(data, keys[:n-1])
		}
		return Get(data, keys...)
	}
	keys := ParseDotPath(path)
	if n := len(keys); n > 0 && keys[n-1] == countSeg {
		return countPathResult(data, keys[:n-1])
	}
	return Get(data, keys...)
}

// GetPointer on an indexed Document.
func (d *Document) GetPointer(pointer string) (value []byte, dataType ValueType, offset int, err error) {
	var buf [maxStackSegs]string
	if keys, ok, perr := parsePointerInto(buf[:0], pointer); perr != nil {
		return nil, NotExist, 0, perr
	} else if ok {
		return d.Get(keys...)
	}
	keys, perr := ParsePointer(pointer)
	if perr != nil {
		return nil, NotExist, 0, perr
	}
	return d.Get(keys...)
}

// GetPath on an indexed Document.
func (d *Document) GetPath(path string) (value []byte, dataType ValueType, offset int, err error) {
	var buf [maxStackSegs]string
	if keys, ok := parseDotPathInto(buf[:0], path); ok {
		if n := len(keys); n > 0 && keys[n-1] == countSeg {
			return d.countPathResult(keys[:n-1])
		}
		return d.Get(keys...)
	}
	keys := ParseDotPath(path)
	if n := len(keys); n > 0 && keys[n-1] == countSeg {
		return d.countPathResult(keys[:n-1])
	}
	return d.Get(keys...)
}
