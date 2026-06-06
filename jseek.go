package jseek

// This file defines the primary public API: Get and the typed convenience
// getters. All functions are allocation-free except GetString (which must copy
// to safely unescape) and GetStringUnsafe / GetBytes which return views into
// the caller's buffer.

// valueAt extracts the value beginning at data[start] (which must point past
// leading whitespace), returning the value bytes, its type, the offset just
// past the value, and an error. It is shared by the stateless Get and the
// indexed Document.Get so both have identical extraction semantics.
func valueAt(data []byte, start int) (value []byte, dataType ValueType, offset int, err error) {
	vs, ve, vt, vok := valueBounds(data, start)
	if !vok {
		return nil, NotExist, 0, ErrMalformedJSON
	}
	if vt == String {
		// vs..ve excludes quotes; offset is just past the closing quote.
		return data[vs:ve], String, ve + 1, nil
	}
	return data[vs:ve], vt, ve, nil
}

// Get extracts the value at the given key path from data.
//
// It returns the raw bytes of the value, its ValueType, the byte offset in data
// just past the end of the value, and an error. For strings the returned bytes
// exclude the surrounding quotes and are NOT unescaped (use GetString for a
// decoded string). For objects and arrays the bytes include their delimiters.
//
// If no keys are provided, Get returns the first JSON value in data, which is
// useful for inspecting array elements or stream fragments.
//
// The returned bytes alias data; they remain valid as long as data is not
// mutated. Get never allocates.
func Get(data []byte, keys ...string) (value []byte, dataType ValueType, offset int, err error) {
	start, ok := seek(data, keys)
	if !ok {
		return nil, NotExist, 0, ErrKeyPathNotFound
	}
	return valueAt(data, start)
}

// GetBytes is like Get but returns only the raw value bytes (aliasing data) and
// an error, discarding the type and offset. It never allocates.
func GetBytes(data []byte, keys ...string) ([]byte, error) {
	v, _, _, err := Get(data, keys...)
	return v, err
}

// GetString extracts the value at the key path as a Go string, decoding JSON
// escape sequences and unicode. This allocates to hold the decoded result. If
// the value is not a string, ErrUnexpectedType is returned.
func GetString(data []byte, keys ...string) (string, error) {
	v, vt, _, err := Get(data, keys...)
	if err != nil {
		return "", err
	}
	if vt != String {
		return "", ErrUnexpectedType
	}
	// Decode escapes. If there are none, a single allocation via string().
	out := unescapeInto(nil, v)
	return string(out), nil
}

// GetStringUnsafe extracts a string value WITHOUT decoding escape sequences and
// without allocating, returning a string that aliases the underlying bytes of
// data. The returned string is only valid while data is unmodified and live.
// Use it for comparisons and lookups on hot paths where you control data's
// lifetime. If the value contains escapes you will see them verbatim.
func GetStringUnsafe(data []byte, keys ...string) (string, error) {
	v, vt, _, err := Get(data, keys...)
	if err != nil {
		return "", err
	}
	if vt != String {
		return "", ErrUnexpectedType
	}
	return btosUnsafe(v), nil
}

// GetInt extracts an integer value at the key path. If the value is a number
// with a fraction or exponent, ErrUnexpectedType is returned. It never
// allocates.
func GetInt(data []byte, keys ...string) (int64, error) {
	v, vt, _, err := Get(data, keys...)
	if err != nil {
		return 0, err
	}
	if vt != Number {
		return 0, ErrUnexpectedType
	}
	return parseInt(v)
}

// GetFloat extracts a floating-point value at the key path.
func GetFloat(data []byte, keys ...string) (float64, error) {
	v, vt, _, err := Get(data, keys...)
	if err != nil {
		return 0, err
	}
	if vt != Number {
		return 0, ErrUnexpectedType
	}
	return parseFloat(v)
}

// GetBoolean extracts a boolean value at the key path.
func GetBoolean(data []byte, keys ...string) (bool, error) {
	v, vt, _, err := Get(data, keys...)
	if err != nil {
		return false, err
	}
	if vt != Boolean {
		return false, ErrUnexpectedType
	}
	return parseBool(v)
}

// Exists reports whether the given key path resolves to a value in data.
func Exists(data []byte, keys ...string) bool {
	_, ok := seek(data, keys)
	return ok
}
