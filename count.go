package jseek

import "strconv"

// CountSeg is the native path segment that requests a container count:
// Get(data, "users", jseek.CountSeg) returns the number of elements as a
// Number value. It contains a NUL byte, which can never appear in a decoded
// JSON object key, so it cannot collide with user data (a literal "#" key
// stays addressable). The dotted-path surface syntax is a trailing ".#"
// (see ParseDotPath / GetPath).
const CountSeg = "\x00#"

// countSeg is the internal alias used by the navigation hooks.
const countSeg = CountSeg

// Container cardinality without materialization. Count answers "how many
// elements / members" by skipping each entry with the same SWAR/SIMD-backed
// skip primitives the navigator uses — no decoding, no allocation. It pairs
// with the "#" path suffix so dotted paths can ask for sizes inline, matching
// the query-syntax expectations set by other extractors.

// Count returns the number of elements in the array, or the number of members
// in the object, at the given key path (or the top-level value if no keys are
// given). It never allocates.
//
// Errors: ErrKeyPathNotFound if the path is missing, ErrUnexpectedType if the
// value is neither an array nor an object, ErrMalformedJSON if the container
// is truncated or malformed where it had to be inspected.
func Count(data []byte, keys ...string) (int, error) {
	start, ok := seek(data, keys)
	if !ok {
		return 0, ErrKeyPathNotFound
	}
	return countAt(data, start)
}

// Count on an indexed Document.
func (d *Document) Count(keys ...string) (int, error) {
	start, ok := d.seekIndexed(keys)
	if !ok {
		return 0, ErrKeyPathNotFound
	}
	return countAt(d.data, start)
}

func countAt(data []byte, start int) (int, error) {
	if start >= len(data) {
		return 0, ErrUnexpectedType
	}
	switch data[start] {
	case '[':
		return countArray(data, start)
	case '{':
		return countObject(data, start)
	default:
		return 0, ErrUnexpectedType
	}
}

func countArray(data []byte, i int) (int, error) {
	n := len(data)
	i = skipWhitespace(data, i+1)
	if i < n && data[i] == ']' {
		return 0, nil
	}
	count := 0
	for i < n {
		var ok bool
		i, ok = skipValue(data, i)
		if !ok {
			return 0, ErrMalformedJSON
		}
		count++
		i = skipWhitespace(data, i)
		if i >= n {
			return 0, ErrMalformedJSON
		}
		switch data[i] {
		case ',':
			i = skipWhitespace(data, i+1)
		case ']':
			return count, nil
		default:
			return 0, ErrMalformedJSON
		}
	}
	return 0, ErrMalformedJSON
}

func countObject(data []byte, i int) (int, error) {
	n := len(data)
	i = skipWhitespace(data, i+1)
	if i < n && data[i] == '}' {
		return 0, nil
	}
	count := 0
	for i < n {
		if data[i] != '"' {
			return 0, ErrMalformedJSON
		}
		var ok bool
		i, ok = skipString(data, i)
		if !ok {
			return 0, ErrMalformedJSON
		}
		i = skipWhitespace(data, i)
		if i >= n || data[i] != ':' {
			return 0, ErrMalformedJSON
		}
		i = skipWhitespace(data, i+1)
		i, ok = skipValue(data, i)
		if !ok {
			return 0, ErrMalformedJSON
		}
		count++
		i = skipWhitespace(data, i)
		if i >= n {
			return 0, ErrMalformedJSON
		}
		switch data[i] {
		case ',':
			i = skipWhitespace(data, i+1)
		case '}':
			return count, nil
		default:
			return 0, ErrMalformedJSON
		}
	}
	return 0, ErrMalformedJSON
}

// countPathResult adapts Count to the Get result shape for a trailing "#"
// path segment. The count is materialized as decimal bytes (one small
// allocation); offset is 0 because the value is synthesized, not located in
// data. All other Get semantics (type, error) are preserved.
func countPathResult(data []byte, keys []string) ([]byte, ValueType, int, error) {
	c, err := Count(data, keys...)
	if err != nil {
		return nil, NotExist, 0, err
	}
	return strconv.AppendInt(nil, int64(c), 10), Number, 0, nil
}

func (d *Document) countPathResult(keys []string) ([]byte, ValueType, int, error) {
	c, err := d.Count(keys...)
	if err != nil {
		return nil, NotExist, 0, err
	}
	return strconv.AppendInt(nil, int64(c), 10), Number, 0, nil
}