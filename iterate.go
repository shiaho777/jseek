package jseek

// ArrayEach calls cb for each element of the array located at the given key
// path (or the top-level value if no keys are given). It never allocates. The
// value passed to cb aliases data: for strings the quotes are excluded; for
// objects/arrays the delimiters are included.
//
// Iteration stops early if cb returns false. The returned error is non-nil only
// when the path is missing or the array is malformed.
func ArrayEach(data []byte, cb func(value []byte, dataType ValueType, offset int) bool, keys ...string) error {
	start, ok := seek(data, keys)
	if !ok {
		return ErrKeyPathNotFound
	}
	if start >= len(data) || data[start] != '[' {
		return ErrUnexpectedType
	}
	i := skipWhitespace(data, start+1)
	if i < len(data) && data[i] == ']' {
		return nil
	}
	for i < len(data) {
		vs, ve, vt, vok := valueBounds(data, i)
		if !vok {
			return ErrMalformedJSON
		}
		var value []byte
		var next int
		if vt == String {
			value = data[vs:ve]
			next = ve + 1
		} else {
			value = data[vs:ve]
			next = ve
		}
		if !cb(value, vt, vs) {
			return nil
		}
		i = skipWhitespace(data, next)
		if i >= len(data) {
			return ErrMalformedJSON
		}
		switch data[i] {
		case ',':
			i = skipWhitespace(data, i+1)
		case ']':
			return nil
		default:
			return ErrMalformedJSON
		}
	}
	return ErrMalformedJSON
}

// ObjectEach calls cb for each key/value member of the object located at the
// given key path (or the top-level value if no keys are given). The key bytes
// are the raw (still-escaped) string content without quotes; the value follows
// the same aliasing rules as ArrayEach. It never allocates.
//
// Iteration stops early if cb returns false.
func ObjectEach(data []byte, cb func(key []byte, value []byte, dataType ValueType, offset int) bool, keys ...string) error {
	start, ok := seek(data, keys)
	if !ok {
		return ErrKeyPathNotFound
	}
	if start >= len(data) || data[start] != '{' {
		return ErrUnexpectedType
	}
	i := skipWhitespace(data, start+1)
	if i < len(data) && data[i] == '}' {
		return nil
	}
	for i < len(data) {
		if data[i] != '"' {
			return ErrMalformedJSON
		}
		ks := i + 1
		ke, sok := skipString(data, i)
		if !sok {
			return ErrMalformedJSON
		}
		key := data[ks : ke-1]
		i = skipWhitespace(data, ke)
		if i >= len(data) || data[i] != ':' {
			return ErrMalformedJSON
		}
		i = skipWhitespace(data, i+1)
		vs, ve, vt, vok := valueBounds(data, i)
		if !vok {
			return ErrMalformedJSON
		}
		var value []byte
		var next int
		if vt == String {
			value = data[vs:ve]
			next = ve + 1
		} else {
			value = data[vs:ve]
			next = ve
		}
		if !cb(key, value, vt, vs) {
			return nil
		}
		i = skipWhitespace(data, next)
		if i >= len(data) {
			return ErrMalformedJSON
		}
		switch data[i] {
		case ',':
			i = skipWhitespace(data, i+1)
		case '}':
			return nil
		default:
			return ErrMalformedJSON
		}
	}
	return ErrMalformedJSON
}
