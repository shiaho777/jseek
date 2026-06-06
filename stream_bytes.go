package jseek

// In-memory streaming: when the whole input is already a []byte, there is no
// need for a buffered reader or any copying. StreamBytes walks the slice and
// hands the callback a sub-slice for each top-level element, aliasing the input
// with zero allocation. This is the fast path for data that fits in RAM
// (NDJSON files mmap'd or read whole, top-level arrays in memory); the
// reader-based Decoder remains for truly unbounded streams.

// StreamBytes invokes cb for each top-level element of data, where data is
// either a top-level array ([...]) or a sequence of whitespace/newline
// separated values (NDJSON). Each element slice aliases data and is valid for
// the lifetime of data. cb returning a non-nil error stops iteration and that
// error is returned; a clean walk returns nil.
//
// Unlike the reader-based Decoder, StreamBytes performs no buffering and no
// allocation — it is the preferred entry point when the input is in memory.
func StreamBytes(data []byte, cb func(value []byte) error) error {
	i := skipWhitespace(data, 0)
	if i >= len(data) {
		return nil
	}

	inArray := false
	if data[i] == '[' {
		inArray = true
		i = skipWhitespace(data, i+1)
		if i < len(data) && data[i] == ']' {
			return nil // empty array
		}
	}

	for i < len(data) {
		// Locate one complete value.
		vs, ve, _, ok := valueBounds(data, i)
		if !ok {
			return ErrMalformedJSON
		}
		// For strings, valueBounds strips the quotes; recover the full token so
		// the element handed to cb is self-contained valid JSON.
		elemStart := i
		var elemEnd int
		if data[i] == '"' {
			elemEnd = ve + 1 // include closing quote
		} else {
			elemEnd = ve
		}
		_ = vs
		if cberr := cb(data[elemStart:elemEnd]); cberr != nil {
			return cberr
		}

		i = skipWhitespace(data, elemEnd)
		if inArray {
			if i >= len(data) {
				return ErrMalformedJSON // unterminated array
			}
			switch data[i] {
			case ',':
				i = skipWhitespace(data, i+1)
			case ']':
				return nil
			default:
				return ErrMalformedJSON
			}
		} else {
			// NDJSON / whitespace separated: skipWhitespace already advanced.
			if i >= len(data) {
				return nil
			}
		}
	}
	return nil
}
