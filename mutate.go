package jseek

// Mutation operations: Set and Delete.
//
// Changing a value's length shifts the bytes after it, so a mutation must
// produce a new document — the caller's input is never modified. The key
// observation that keeps this cheap: a Set is globally just "replace one
// contiguous byte range with new bytes" and a Delete is "remove one contiguous
// byte range". Nesting does not change that — splicing a child back into its
// parent only re-embeds bytes that were never touched. So both operations
// locate a single edit region in ONE downward pass over the path and emit the
// result with a SINGLE allocation (the result buffer), rather than allocating
// an intermediate slice at every level of the path.
//
// The Append* variants write that single result into a caller-provided buffer,
// so a hot loop that reuses one scratch buffer mutates with amortized zero
// allocation. Set/Delete are thin wrappers that target a fresh buffer.
//
// Correctness is validated by differential fuzzing against encoding/json
// (FuzzSetAgainstStdlib, FuzzDeleteAgainstStdlib).

// Set returns a copy of data with the value at the given key path replaced by
// setValue. If the path does not exist, Set creates the missing object keys
// (building nested objects as needed) and inserts setValue. Existing array
// elements may be replaced, but arrays are not extended and missing array
// indices in a to-be-created path are reported as ErrKeyPathNotFound.
//
// setValue must itself be valid JSON (e.g. []byte(`"text"`), []byte("42"),
// []byte(`{"a":1}`)). The caller's data is never modified.
func Set(data []byte, setValue []byte, keys ...string) ([]byte, error) {
	// Presize so the result fits without reallocating: worst case keeps all of
	// data, adds setValue, and (for a created path) emits each key escaped plus
	// its structural punctuation.
	keyLen := 0
	for _, k := range keys {
		keyLen += len(k)
	}
	cap := len(data) + len(setValue) + 2*keyLen + 4*len(keys) + 16
	return AppendSet(make([]byte, 0, cap), data, setValue, keys...)
}

// AppendSet performs the same operation as Set but appends the resulting
// document to dst and returns the extended slice, so a caller can reuse one
// buffer across many mutations for amortized zero allocation:
//
//	buf = buf[:0]
//	buf, err = AppendSet(buf, data, val, "a", "b")
//
// The returned slice never aliases data. On error, dst is left logically
// unchanged (its first len(dst) bytes are preserved) and the returned slice is
// nil.
func AppendSet(dst []byte, data []byte, setValue []byte, keys ...string) ([]byte, error) {
	if len(keys) == 0 {
		return append(dst, setValue...), nil
	}
	start := skipWhitespace(data, 0)
	if _, ok := skipValue(data, start); !ok {
		// Empty or whitespace-only input: synthesize a document from scratch.
		if err := validateBuildKeys(keys); err != nil {
			return nil, err
		}
		return appendNested(dst, keys, setValue), nil
	}
	e, err := planSet(data, start, keys)
	if err != nil {
		return nil, err
	}
	out := append(dst, data[:e.cut0]...)
	if e.replace {
		out = append(out, setValue...)
	} else {
		// Insert `"key": <nested>` (with a leading comma into a non-empty
		// object). restKeys was already validated to contain no array indices.
		if !e.insertEmpty {
			out = append(out, ',')
		}
		out = appendJSONString(out, e.insertKey)
		out = append(out, ':')
		out = appendNested(out, e.restKeys, setValue)
	}
	out = append(out, data[e.cut1:]...)
	return out, nil
}

// setEdit describes the single contiguous edit a Set resolves to: replace the
// byte range [cut0,cut1) of data with either setValue (replace) or, for a
// path whose final segment's key is absent, an inserted `"key":<nested>` member
// (insert; cut0==cut1 marks a pure insertion point, except for an empty object
// where [cut0,cut1) spans the interior so existing interior whitespace is
// dropped, matching prior behavior).
type setEdit struct {
	cut0, cut1  int
	replace     bool
	insertEmpty bool
	insertKey   string
	restKeys    []string
}

// planSet walks the path over data (in one pass, descending into existing
// containers) and returns the single edit that realizes the Set.
func planSet(data []byte, start int, keys []string) (setEdit, error) {
	var e setEdit
	i := start
	for ki := 0; ki < len(keys); ki++ {
		key := keys[ki]
		if i >= len(data) {
			return e, ErrKeyPathNotFound
		}
		if n, isIdx := parseArrayIndex(key); isIdx {
			if data[i] != '[' {
				return e, ErrUnexpectedType
			}
			es, ok := findIndex(data, i, n)
			if !ok {
				return e, ErrKeyPathNotFound // arrays are not extended
			}
			i = es
			continue
		}
		if data[i] != '{' {
			return e, ErrUnexpectedType
		}
		vs, found := findKey(data, i, key)
		if found {
			i = vs
			continue
		}
		// Key absent at this level: insert `"key": buildNested(rest)` into the
		// object that begins at data[i]. The rest of the path must be all
		// object keys (arrays cannot be created).
		rest := keys[ki+1:]
		if err := validateBuildKeys(rest); err != nil {
			return e, err
		}
		oi := i
		objEnd, ok := skipObject(data, oi)
		if !ok {
			return e, ErrMalformedJSON
		}
		closeBrace := objEnd - 1 // index of '}'
		j := skipWhitespace(data, oi+1)
		empty := j < len(data) && data[j] == '}'
		e.replace = false
		e.insertEmpty = empty
		e.insertKey = key
		e.restKeys = rest
		if empty {
			// Replace the interior [`{`+1, `}`) with the new member.
			e.cut0, e.cut1 = oi+1, closeBrace
		} else {
			// Pure insertion just before the closing brace.
			e.cut0, e.cut1 = closeBrace, closeBrace
		}
		return e, nil
	}
	// Path fully resolved to an existing value: replace it.
	ve, ok := skipValue(data, i)
	if !ok {
		return e, ErrMalformedJSON
	}
	e.replace = true
	e.cut0, e.cut1 = i, ve
	return e, nil
}

// validateBuildKeys reports ErrKeyPathNotFound if any segment is an array index,
// since a to-be-created path can only build nested objects.
func validateBuildKeys(keys []string) error {
	for _, k := range keys {
		if _, isIdx := parseArrayIndex(k); isIdx {
			return ErrKeyPathNotFound
		}
	}
	return nil
}

// appendNested appends buildNested(keys, setValue) directly to dst with no
// per-level intermediate allocation: it opens one object per key, writes
// setValue, then closes them. keys must contain no array indices (validated by
// the caller).
func appendNested(dst []byte, keys []string, setValue []byte) []byte {
	for _, k := range keys {
		dst = append(dst, '{')
		dst = appendJSONString(dst, k)
		dst = append(dst, ':')
	}
	dst = append(dst, setValue...)
	for range keys {
		dst = append(dst, '}')
	}
	return dst
}

// Delete returns a copy of data with the value at the given key path removed.
// If the path does not exist, an identical copy of data is returned. With no
// keys, an empty slice is returned. The caller's data is never modified.
func Delete(data []byte, keys ...string) []byte {
	if len(keys) == 0 {
		return []byte{}
	}
	// A delete only removes bytes, so the result never exceeds len(data).
	out, _ := AppendDelete(make([]byte, 0, len(data)), data, keys...)
	return out
}

// AppendDelete performs the same operation as Delete but appends the resulting
// document to dst and returns the extended slice along with whether anything
// was removed. Reuse one buffer across calls for amortized zero allocation. The
// returned slice never aliases data. If the path is not found, the original
// document is appended unchanged and removed is false.
func AppendDelete(dst []byte, data []byte, keys ...string) (out []byte, removed bool) {
	if len(keys) == 0 {
		return dst, true // deleting the whole document yields an empty result
	}
	start := skipWhitespace(data, 0)
	if _, ok := skipValue(data, start); !ok {
		return append(dst, data...), false
	}
	rs, re, ok := planDelete(data, start, keys)
	if !ok {
		return append(dst, data...), false
	}
	out = append(dst, data[:rs]...)
	out = append(out, data[re:]...)
	return out, true
}

// planDelete descends through all but the last path segment (which must exist),
// then locates the contiguous byte range [rs,re) to remove for the final
// segment, including the separating comma so the result stays valid JSON.
func planDelete(data []byte, start int, keys []string) (rs, re int, ok bool) {
	i := start
	for ki := 0; ki < len(keys)-1; ki++ {
		key := keys[ki]
		if i >= len(data) {
			return 0, 0, false
		}
		if n, isIdx := parseArrayIndex(key); isIdx {
			if data[i] != '[' {
				return 0, 0, false
			}
			es, found := findIndex(data, i, n)
			if !found {
				return 0, 0, false
			}
			i = es
			continue
		}
		if data[i] != '{' {
			return 0, 0, false
		}
		vs, found := findKey(data, i, key)
		if !found {
			return 0, 0, false
		}
		i = vs
	}
	if i >= len(data) {
		return 0, 0, false
	}
	last := keys[len(keys)-1]
	if n, isIdx := parseArrayIndex(last); isIdx {
		if data[i] != '[' {
			return 0, 0, false
		}
		return rangeArrayElement(data, i, n)
	}
	if data[i] != '{' {
		return 0, 0, false
	}
	return rangeObjectMember(data, i, last)
}

// rangeObjectMember returns the byte range to remove for member `key` in the
// object beginning at data[oi], absorbing the appropriate comma.
func rangeObjectMember(data []byte, oi int, key string) (rs, re int, ok bool) {
	i := skipWhitespace(data, oi+1)
	if i < len(data) && data[i] == '}' {
		return 0, 0, false // empty object
	}
	prevSep := -1 // index of the comma before the current member, if any
	for i < len(data) {
		if data[i] != '"' {
			return 0, 0, false
		}
		keyStart := i
		ks := i + 1
		ke, sok := skipString(data, i)
		if !sok {
			return 0, 0, false
		}
		match := keyMatches(data, ks, ke-1, key)
		j := skipWhitespace(data, ke)
		if j >= len(data) || data[j] != ':' {
			return 0, 0, false
		}
		j = skipWhitespace(data, j+1)
		valEnd, vok := skipValue(data, j)
		if !vok {
			return 0, 0, false
		}
		afterVal := skipWhitespace(data, valEnd)

		if match {
			if prevSep >= 0 {
				return prevSep, valEnd, true // eat preceding comma .. value end
			}
			if afterVal < len(data) && data[afterVal] == ',' {
				return keyStart, afterVal + 1, true // first member .. following comma
			}
			return keyStart, valEnd, true // only member
		}

		if afterVal >= len(data) {
			return 0, 0, false
		}
		switch data[afterVal] {
		case ',':
			prevSep = afterVal
			i = skipWhitespace(data, afterVal+1)
		case '}':
			return 0, 0, false
		default:
			return 0, 0, false
		}
	}
	return 0, 0, false
}

// rangeArrayElement returns the byte range to remove for element n in the array
// beginning at data[ai], absorbing the appropriate comma.
func rangeArrayElement(data []byte, ai int, n int) (rs, re int, ok bool) {
	i := skipWhitespace(data, ai+1)
	if i < len(data) && data[i] == ']' {
		return 0, 0, false // empty array
	}
	idx := 0
	prevSep := -1
	for i < len(data) {
		elemStart := i
		elemEnd, vok := skipValue(data, i)
		if !vok {
			return 0, 0, false
		}
		afterVal := skipWhitespace(data, elemEnd)

		if idx == n {
			if prevSep >= 0 {
				return prevSep, elemEnd, true
			}
			if afterVal < len(data) && data[afterVal] == ',' {
				return elemStart, afterVal + 1, true
			}
			return elemStart, elemEnd, true
		}

		if afterVal >= len(data) {
			return 0, 0, false
		}
		switch data[afterVal] {
		case ',':
			prevSep = afterVal
			i = skipWhitespace(data, afterVal+1)
			idx++
		case ']':
			return 0, 0, false
		default:
			return 0, 0, false
		}
	}
	return 0, 0, false
}

// appendJSONString appends s to dst as a quoted, escaped JSON string.
func appendJSONString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			dst = append(dst, '\\', '"')
		case '\\':
			dst = append(dst, '\\', '\\')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		case '\b':
			dst = append(dst, '\\', 'b')
		case '\f':
			dst = append(dst, '\\', 'f')
		default:
			if c < 0x20 {
				const hex = "0123456789abcdef"
				dst = append(dst, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xf])
			} else {
				dst = append(dst, c)
			}
		}
	}
	return append(dst, '"')
}
