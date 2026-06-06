package jseek

// This file implements the portable (pure-Go) structural scanner and the
// skip-subtree navigation primitives that the query layer builds on. Every
// function here is allocation-free and never mutates its input.
//
// The SIMD Stage-1 scanner will plug in behind skipString / skipValue without
// changing any caller, because those are the only hot loops that touch every
// byte of skipped data.

// whitespace reports whether c is JSON insignificant whitespace.
func isWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// skipWhitespace returns the index of the first non-whitespace byte at or after
// i. It may return len(data).
func skipWhitespace(data []byte, i int) int {
	for i < len(data) {
		switch data[i] {
		case ' ', '\t', '\n', '\r':
			i++
		default:
			return i
		}
	}
	return i
}

// classify returns the ValueType of the value beginning at data[i], which must
// already point past any leading whitespace. It does not validate the value.
func classify(data []byte, i int) ValueType {
	if i >= len(data) {
		return NotExist
	}
	switch data[i] {
	case '"':
		return String
	case '{':
		return Object
	case '[':
		return Array
	case 't', 'f':
		return Boolean
	case 'n':
		return Null
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return Number
	default:
		return Unknown
	}
}

// skipString expects data[i] == '"' and returns the index just past the closing
// quote, plus true on success. It correctly handles backslash escapes.
//
// The inner scan uses the SWAR (8-bytes-per-step) search for the next quote or
// backslash, so unescaped string content — the bulk of real JSON — is traversed
// at register-parallel speed rather than one byte per iteration.
func skipString(data []byte, i int) (int, bool) {
	// data[i] is the opening quote.
	i++
	n := len(data)
	for i < n {
		j := indexQuoteOrBackslash(data, i)
		if j < 0 {
			return n, false
		}
		if data[j] == '"' {
			return j + 1, true
		}
		// data[j] == '\\': skip the escaped byte and resume scanning.
		i = j + 2
	}
	return i, false
}

// skipNumber returns the index just past a number beginning at data[i]. It is
// permissive: it consumes the maximal run of number bytes and lets typed
// parsing validate. Returns false only if no number byte is present.
func skipNumber(data []byte, i int) (int, bool) {
	start := i
	n := len(data)
	for i < n {
		switch data[i] {
		case '-', '+', '.', 'e', 'E',
			'0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
			i++
		default:
			return i, i > start
		}
	}
	return i, i > start
}

// skipLiteral checks for the literal word (true/false/null) at data[i] and
// returns the index just past it.
func skipLiteral(data []byte, i int, word string) (int, bool) {
	if i+len(word) > len(data) {
		return i, false
	}
	if string(data[i:i+len(word)]) != word {
		return i, false
	}
	return i + len(word), true
}

// skipValue skips the entire JSON value (including any nested object/array
// subtree) beginning at data[i], which must point past leading whitespace. It
// returns the index just past the value and true on success.
//
// This is the workhorse of lazy navigation: it lets the query layer step over
// values it does not care about without descending into them.
func skipValue(data []byte, i int) (int, bool) {
	if i >= len(data) {
		return i, false
	}
	switch data[i] {
	case '"':
		return skipString(data, i)
	case '{', '[':
		return skipContainer(data, i)
	case 't':
		return skipLiteral(data, i, "true")
	case 'f':
		return skipLiteral(data, i, "false")
	case 'n':
		return skipLiteral(data, i, "null")
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return skipNumber(data, i)
	default:
		return i, false
	}
}

// skipContainer skips a full object or array beginning at data[i] (which must
// be '{' or '['), returning the index just past its matching closer and true on
// success.
//
// It is a single flat loop — the fast replacement for the recursive
// skipObject/skipArray descent. Skipping a subtree only requires finding the
// matching closing bracket, so we track depth and treat every byte uniformly:
// a quote hands off to the SWAR string-body scanner (strings may hide
// brackets); an open/close bracket adjusts depth; everything else (numbers,
// literals, whitespace, ':' and ',') is a single i++ with no function call, no
// whitespace sub-scan, no number parse, no grammar validation. That removes the
// ~60% per-token bookkeeping overhead the recursive walk spent on bytes that
// cannot affect where the value ends. On valid JSON the returned offset is
// identical; on malformed input it terminates without panicking (per contract).
//
// NOTE: a SWAR "scan to next of {}[]\"" was prototyped here and measured ~2x
// SLOWER on realistic (string-heavy) JSON: quotes recur every few bytes, so the
// wide scanner pays its per-call setup without skipping a meaningful run. The
// byte-at-a-time gap handling below wins whenever structural bytes are dense,
// which is the common case; string *bodies* (the long runs) already use SWAR.
func skipContainer(data []byte, i int) (int, bool) {
	n := len(data)
	depth := 0
	for i < n {
		switch data[i] {
		case '"':
			// Inline the string-body skip (skipString) to avoid a call frame in
			// the hottest loop: scan for the closing quote with the SWAR
			// quote/backslash scanner, stepping over escaped bytes.
			i++
			for {
				j := indexQuoteOrBackslash(data, i)
				if j < 0 {
					return n, false
				}
				if data[j] == '"' {
					i = j + 1
					break
				}
				i = j + 2 // escaped byte
			}
		case '{', '[':
			depth++
			i++
		case '}', ']':
			depth--
			i++
			if depth == 0 {
				return i, true
			}
		default:
			i++
		}
	}
	return i, false
}

// skipObject skips a full object beginning at data[i] == '{'. It delegates to
// the flat skipContainer scan; on valid JSON the end offset is identical to a
// full member-by-member walk. Returns the index just past the closing brace.
func skipObject(data []byte, i int) (int, bool) {
	return skipContainer(data, i)
}

// skipArray skips a full array beginning at data[i] == '['. It delegates to the
// flat skipContainer scan. Returns the index just past the closing bracket.
func skipArray(data []byte, i int) (int, bool) {
	return skipContainer(data, i)
}

// valueBounds returns the [start,end) byte range of the value beginning at
// data[i] (past leading whitespace) and its type. For strings, the range
// excludes the surrounding quotes to match GetString semantics; for all other
// types the range is the verbatim token (objects/arrays include their
// delimiters). ok is false on malformed input.
func valueBounds(data []byte, i int) (start, end int, vt ValueType, ok bool) {
	vt = classify(data, i)
	switch vt {
	case String:
		var e int
		e, ok = skipString(data, i)
		if !ok {
			return i, e, String, false
		}
		// strip the quotes
		return i + 1, e - 1, String, true
	case Object:
		var e int
		e, ok = skipObject(data, i)
		return i, e, Object, ok
	case Array:
		var e int
		e, ok = skipArray(data, i)
		return i, e, Array, ok
	case Number:
		var e int
		e, ok = skipNumber(data, i)
		return i, e, Number, ok
	case Boolean:
		word := "true"
		if data[i] == 'f' {
			word = "false"
		}
		var e int
		e, ok = skipLiteral(data, i, word)
		return i, e, Boolean, ok
	case Null:
		var e int
		e, ok = skipLiteral(data, i, "null")
		return i, e, Null, ok
	default:
		return i, i, NotExist, false
	}
}
