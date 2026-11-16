package jseek

// JSON validation. Unlike the extraction paths — which are permissive about
// bytes they do not need to interpret — Valid checks the full JSON syntax:
// literal spelling, number grammar (no leading zeros, required
// fraction/exponent digits), string escape legality, and container structure.
// It is the gateway primitive: validate untrusted input once, then extract
// with the lazy API.
//
// Valid never allocates and never mutates its input.

// Valid reports whether data is exactly one well-formed JSON value,
// optionally surrounded by JSON whitespace. It agrees with
// encoding/json.Valid on all inputs: like the stdlib it is a syntax check, so
// lone \uD800-style surrogates and invalid UTF-8 bytes inside strings are
// accepted (decoders replace them later). Duplicate object keys are accepted
// (RFC 8259 leaves them implementation-defined). Valid never allocates.
func Valid(data []byte) bool {
	i := skipWhitespace(data, 0)
	i, ok := validValue(data, i, 0)
	if !ok {
		return false
	}
	i = skipWhitespace(data, i)
	return i == len(data)
}

// maxValidDepth bounds container nesting. encoding/json's scanner caps at
// 10000; staying below it keeps Valid in agreement with json.Valid while
// bounding stack use (the validator recurses once per nesting level).
const maxValidDepth = 10000

func validValue(data []byte, i, depth int) (int, bool) {
	if i >= len(data) {
		return i, false
	}
	switch data[i] {
	case '"':
		return validString(data, i)
	case '{':
		if depth >= maxValidDepth {
			return i, false
		}
		return validObject(data, i, depth+1)
	case '[':
		if depth >= maxValidDepth {
			return i, false
		}
		return validArray(data, i, depth+1)
	case 't':
		return skipTrue(data, i)
	case 'f':
		return skipFalse(data, i)
	case 'n':
		return skipNull(data, i)
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return validNumber(data, i)
	default:
		return i, false
	}
}

func validObject(data []byte, i, depth int) (int, bool) {
	n := len(data)
	i = skipWhitespace(data, i+1)
	if i < n && data[i] == '}' {
		return i + 1, true
	}
	for {
		if i >= n || data[i] != '"' {
			return i, false
		}
		var ok bool
		i, ok = validString(data, i)
		if !ok {
			return i, false
		}
		i = skipWhitespace(data, i)
		if i >= n || data[i] != ':' {
			return i, false
		}
		i = skipWhitespace(data, i+1)
		i, ok = validValue(data, i, depth)
		if !ok {
			return i, false
		}
		i = skipWhitespace(data, i)
		if i >= n {
			return i, false
		}
		switch data[i] {
		case ',':
			i = skipWhitespace(data, i+1)
		case '}':
			return i + 1, true
		default:
			return i, false
		}
	}
}

func validArray(data []byte, i, depth int) (int, bool) {
	n := len(data)
	i = skipWhitespace(data, i+1)
	if i < n && data[i] == ']' {
		return i + 1, true
	}
	for {
		var ok bool
		i, ok = validValue(data, i, depth)
		if !ok {
			return i, false
		}
		i = skipWhitespace(data, i)
		if i >= n {
			return i, false
		}
		switch data[i] {
		case ',':
			i = skipWhitespace(data, i+1)
		case ']':
			return i + 1, true
		default:
			return i, false
		}
	}
}

// validString validates a JSON string beginning at data[i] == '"', checking
// escape legality, surrogate pairing, and UTF-8 well-formedness of the raw
// content. Returns the index just past the closing quote.
func validString(data []byte, i int) (int, bool) {
	n := len(data)
	p := i + 1
	for p < n {
		c := data[p]
		if c == '"' {
			return p + 1, true
		}
		if c == '\\' {
			if p+1 >= n {
				return n, false
			}
			switch data[p+1] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				p += 2
			case 'u':
				// Four hex digits are required; the code point itself is
				// not range-checked (stdlib accepts lone surrogates here).
				if _, ok := parseHex4(data, p+2); !ok {
					return n, false
				}
				p += 6
			default:
				return n, false
			}
			continue
		}
		if c < 0x20 {
			return n, false // unescaped control byte
		}
		// Bytes >= 0x80 are accepted verbatim, matching json.Valid; UTF-8
		// well-formedness is a decoder concern (see validStringUTF8 for the
		// strict variant).
		p++
	}
	return n, false
}

// validStringUTF8 is the strict RFC 8259 string check: like validString but
// additionally requiring well-formed UTF-8 and paired surrogates. Reserved
// for a future strict-validation mode; kept exercised by unit tests.
func validStringUTF8(data []byte, i int) (int, bool) {
	n := len(data)
	p := i + 1
	for p < n {
		c := data[p]
		if c == '"' {
			return p + 1, true
		}
		if c == '\\' {
			if p+1 >= n {
				return n, false
			}
			switch data[p+1] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				p += 2
			case 'u':
				r, ok := parseHex4(data, p+2)
				if !ok {
					return n, false
				}
				p += 6
				if r >= 0xD800 && r <= 0xDBFF {
					if p+6 > n || data[p] != '\\' || data[p+1] != 'u' {
						return n, false
					}
					r2, ok := parseHex4(data, p+2)
					if !ok || r2 < 0xDC00 || r2 > 0xDFFF {
						return n, false
					}
					p += 6
				} else if r >= 0xDC00 && r <= 0xDFFF {
					return n, false // lone low surrogate
				}
			default:
				return n, false
			}
			continue
		}
		if c < 0x20 {
			return n, false
		}
		if c < 0x80 {
			p++
			continue
		}
		size := utf8SeqLen(c)
		if size == 0 || p+size > n {
			return n, false
		}
		switch size {
		case 2:
			if !utf8Cont(data[p+1]) {
				return n, false
			}
		case 3:
			if !utf8Cont(data[p+1]) || !utf8Cont(data[p+2]) {
				return n, false
			}
			if (c == 0xE0 && data[p+1] < 0xA0) || (c == 0xED && data[p+1] >= 0xA0) {
				return n, false // overlong / surrogate
			}
		case 4:
			if !utf8Cont(data[p+1]) || !utf8Cont(data[p+2]) || !utf8Cont(data[p+3]) {
				return n, false
			}
			if (c == 0xF0 && data[p+1] < 0x90) || (c == 0xF4 && data[p+1] >= 0x90) {
				return n, false // overlong / beyond U+10FFFF
			}
		}
		p += size
	}
	return n, false
}

// utf8SeqLen returns the length of the UTF-8 sequence starting with lead
// byte c, or 0 if c cannot start a sequence.
func utf8SeqLen(c byte) int {
	switch {
	case c >= 0xC2 && c <= 0xDF:
		return 2
	case c >= 0xE0 && c <= 0xEF:
		return 3
	case c >= 0xF0 && c <= 0xF4:
		return 4
	default:
		return 0
	}
}

func utf8Cont(c byte) bool { return c >= 0x80 && c <= 0xBF }

// parseHex4 reads exactly four hex digits at data[i:].
func parseHex4(data []byte, i int) (rune, bool) {
	if i+4 > len(data) {
		return 0, false
	}
	var r rune
	for k := 0; k < 4; k++ {
		c := data[i+k]
		var d rune
		switch {
		case c >= '0' && c <= '9':
			d = rune(c - '0')
		case c >= 'a' && c <= 'f':
			d = rune(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = rune(c-'A') + 10
		default:
			return 0, false
		}
		r = r<<4 | d
	}
	return r, true
}

// validNumber validates the RFC 8259 number grammar at data[i]:
// -?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?
func validNumber(data []byte, i int) (int, bool) {
	n := len(data)
	start := i
	if data[i] == '-' {
		i++
		if i >= n {
			return i, false
		}
	}
	if data[i] == '0' {
		i++
		if i < n && data[i] >= '0' && data[i] <= '9' {
			return i, false // leading zero
		}
	} else if data[i] >= '1' && data[i] <= '9' {
		for i < n && data[i] >= '0' && data[i] <= '9' {
			i++
		}
	} else {
		return i, false
	}
	if i < n && data[i] == '.' {
		i++
		if i >= n || data[i] < '0' || data[i] > '9' {
			return i, false
		}
		for i < n && data[i] >= '0' && data[i] <= '9' {
			i++
		}
	}
	if i < n && (data[i] == 'e' || data[i] == 'E') {
		i++
		if i < n && (data[i] == '+' || data[i] == '-') {
			i++
		}
		if i >= n || data[i] < '0' || data[i] > '9' {
			return i, false
		}
		for i < n && data[i] >= '0' && data[i] <= '9' {
			i++
		}
	}
	return i, i > start
}