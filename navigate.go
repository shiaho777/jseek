package jseek

// Navigation locates the value addressed by a single path segment within the
// container that begins at data[i]. These functions are the lazy core: they
// walk only the keys/elements they must, skipping every unrelated subtree via
// skipValue.

// keyMatches reports whether the JSON string occupying data[start:end] (the raw
// bytes between the quotes, possibly containing escapes) equals the plain Go
// string key. The fast path is a direct byte comparison when there are no
// escapes; otherwise it decodes escapes on the fly without allocating.
func keyMatches(data []byte, start, end int, key string) bool {
	// Single forward pass: compare byte-for-byte against key. The overwhelming
	// majority of JSON keys contain no escapes, so we want that path to touch
	// each byte exactly once. The moment we see a backslash we hand off to the
	// escape-aware comparator for the remainder.
	n := end - start
	ki := 0
	for j := 0; j < n; j++ {
		c := data[start+j]
		if c == '\\' {
			// Escapes present: compare the full raw content the slow way.
			return escapedEquals(data[start:end], key)
		}
		if ki >= len(key) || key[ki] != c {
			return false
		}
		ki++
	}
	return ki == len(key)
}

// scanKey scans a JSON object key beginning at data[i] (data[i] == '"') while
// simultaneously comparing it against the target key, in a single pass. It
// returns the index just past the closing quote, whether the key matched, and
// whether the scan succeeded.
//
// This fuses what would otherwise be two passes (skipString + keyMatches) and
// uses a scalar inner loop: object keys are typically short, so the per-call
// setup of the SWAR scanner does not amortize and a tight scalar compare with
// early mismatch bail is faster. Values, which can be long, still use the SWAR
// skipString. On encountering an escape it defers to the exact escape-aware
// comparison.
func scanKey(data []byte, i int, key string) (keyEnd int, matched bool, ok bool) {
	n := len(data)
	p := i + 1
	ki := 0
	klen := len(key)
	matched = true
	for p < n {
		c := data[p]
		if c == '"' {
			return p + 1, matched && ki == klen, true
		}
		if c == '\\' {
			// Escapes present: fall back to the accurate, allocation-free path.
			ke, sok := skipString(data, i)
			if !sok {
				return ke, false, false
			}
			return ke, escapedEquals(data[i+1:ke-1], key), true
		}
		if matched {
			if ki >= klen || key[ki] != c {
				matched = false
			} else {
				ki++
			}
		}
		p++
	}
	return p, false, false // unterminated string (malformed)
}

// findKey locates the value for key within the object beginning at data[oi]
// (data[oi] must be '{'). It returns the index of the start of the value (past
// whitespace) and true if found.
func findKey(data []byte, oi int, key string) (int, bool) {
	i := skipWhitespace(data, oi+1)
	if i < len(data) && data[i] == '}' {
		return i, false
	}
	for i < len(data) {
		if data[i] != '"' {
			return i, false
		}
		ke, match, ok := scanKey(data, i, key)
		if !ok {
			return ke, false
		}
		i = skipWhitespace(data, ke)
		if i >= len(data) || data[i] != ':' {
			return i, false
		}
		i = skipWhitespace(data, i+1)
		if match {
			return i, true
		}
		// Skip this value and continue to the next member.
		if i, ok = skipValue(data, i); !ok {
			return i, false
		}
		i = skipWhitespace(data, i)
		if i >= len(data) {
			return i, false
		}
		switch data[i] {
		case ',':
			i = skipWhitespace(data, i+1)
		case '}':
			return i, false
		default:
			return i, false
		}
	}
	return i, false
}

// findIndex locates element n (zero-based) within the array beginning at
// data[ai] (data[ai] must be '['). Returns the start index of the element value
// and true if found.
func findIndex(data []byte, ai int, n int) (int, bool) {
	i := skipWhitespace(data, ai+1)
	if i < len(data) && data[i] == ']' {
		return i, false
	}
	idx := 0
	for i < len(data) {
		if idx == n {
			return i, true
		}
		var ok bool
		if i, ok = skipValue(data, i); !ok {
			return i, false
		}
		i = skipWhitespace(data, i)
		if i >= len(data) {
			return i, false
		}
		switch data[i] {
		case ',':
			i = skipWhitespace(data, i+1)
			idx++
		case ']':
			return i, false
		default:
			return i, false
		}
	}
	return i, false
}

// parseArrayIndex parses a key of the form "[N]" into the integer N. The second
// return value is false if the key is not an array-index expression.
func parseArrayIndex(key string) (int, bool) {
	if len(key) < 3 || key[0] != '[' || key[len(key)-1] != ']' {
		return 0, false
	}
	digits := key[1 : len(key)-1]
	if len(digits) == 0 {
		return 0, false
	}
	n := 0
	for j := 0; j < len(digits); j++ {
		c := digits[j]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// seek walks the full key path starting at data[0] and returns the start index
// of the located value (past whitespace) along with success.
func seek(data []byte, keys []string) (int, bool) {
	i := skipWhitespace(data, 0)
	for _, key := range keys {
		if i >= len(data) {
			return i, false
		}
		if n, isIdx := parseArrayIndex(key); isIdx {
			if data[i] != '[' {
				return i, false
			}
			var ok bool
			if i, ok = findIndex(data, i, n); !ok {
				return i, false
			}
		} else {
			if data[i] != '{' {
				return i, false
			}
			var ok bool
			if i, ok = findKey(data, i, key); !ok {
				return i, false
			}
		}
	}
	return i, true
}
