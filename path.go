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
func ParseDotPath(path string) []string {
	if path == "" {
		return nil
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

// GetPointer extracts the value addressed by an RFC 6901 JSON Pointer.
func GetPointer(data []byte, pointer string) (value []byte, dataType ValueType, offset int, err error) {
	keys, perr := ParsePointer(pointer)
	if perr != nil {
		return nil, NotExist, 0, perr
	}
	return Get(data, keys...)
}

// GetPath extracts the value addressed by a dotted path like "a.b[0].c".
func GetPath(data []byte, path string) (value []byte, dataType ValueType, offset int, err error) {
	return Get(data, ParseDotPath(path)...)
}

// GetPointer on an indexed Document.
func (d *Document) GetPointer(pointer string) (value []byte, dataType ValueType, offset int, err error) {
	keys, perr := ParsePointer(pointer)
	if perr != nil {
		return nil, NotExist, 0, perr
	}
	return d.Get(keys...)
}

// GetPath on an indexed Document.
func (d *Document) GetPath(path string) (value []byte, dataType ValueType, offset int, err error) {
	return d.Get(ParseDotPath(path)...)
}
