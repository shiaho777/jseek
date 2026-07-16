package jseek

// Stage-2 navigation over a Document's structural index. The key property: to
// step over a nested object/array we scan the (small) structural-offset array
// tracking brace depth, instead of re-scanning every byte of the subtree. The
// single Stage-1 cost is therefore amortized across all queries on a Document.
//
// Because string bodies record no structurals (only the opening quote is
// indexed), a depth scan never sees a brace/bracket that lives inside a string,
// so the depth bookkeeping is exact.

// skipValueIndex takes vj, the position in the structural array of the first
// structural at or after a value, and returns the position of the structural
// that terminates the value's member/element (a ',', '}' or ']').
//
// The hot loop reads only the packed kind from the sequential index array, so
// it never touches the document bytes — keeping the working set in cache even
// when skipping a large subtree.
func (d *Document) skipValueIndex(vj int) int {
	if d.hasTape {
		return d.skipValueIndexTape(vj)
	}
	s := d.structurals
	if vj >= len(s) {
		return vj
	}
	switch entryKind(s[vj]) {
	case kObrace, kObrack:
		depth := 0
		for k := vj; k < len(s); k++ {
			switch entryKind(s[k]) {
			case kObrace, kObrack:
				depth++
			case kCbrace, kCbrack:
				depth--
				if depth == 0 {
					return k + 1
				}
			}
		}
		return len(s) // unterminated container (malformed)
	case kQuote:
		// String value occupies one structural; terminator is the next.
		return vj + 1
	default:
		// ',', '}' or ']' already sits here: the value was a scalar with no
		// structural of its own, so this entry is the terminator.
		return vj
	}
}

// findKeyIndexed locates key within the object whose '{' is at structurals[si].
// It returns the byte offset where the value begins (past whitespace), the
// structural-array position of the value's first structural (for descent), and
// whether the key was found.
func (d *Document) findKeyIndexed(si int, key string) (valByteStart int, valStruct int, found bool) {
	s := d.structurals
	j := si + 1
	if j >= len(s) {
		return 0, 0, false
	}
	// Emptiness must be judged from the bytes: a scalar first member produces
	// no structural of its own, so "next structural is }" does not imply empty.
	if b := skipWhitespace(d.data, entryOffset(s[si])+1); b < len(d.data) && d.data[b] == '}' {
		return 0, 0, false // empty object
	}
	for j < len(s) {
		if entryKind(s[j]) != kQuote {
			return 0, 0, false // expected a key
		}
		keyOpen := entryOffset(s[j])
		_, match, ok := scanKey(d.data, keyOpen, key)
		if !ok {
			return 0, 0, false
		}

		cj := j + 1
		if cj >= len(s) || entryKind(s[cj]) != kColon {
			return 0, 0, false
		}
		vStart := skipWhitespace(d.data, entryOffset(s[cj])+1)
		if match {
			return vStart, cj + 1, true
		}
		tj := d.skipValueIndex(cj + 1)
		if tj >= len(s) {
			return 0, 0, false
		}
		switch entryKind(s[tj]) {
		case kComma:
			j = tj + 1
		case kCbrace:
			return 0, 0, false
		default:
			return 0, 0, false
		}
	}
	return 0, 0, false
}

// findIndexIndexed locates element n within the array whose '[' is at
// structurals[si].
func (d *Document) findIndexIndexed(si int, n int) (valByteStart int, valStruct int, found bool) {
	s := d.structurals
	if si+1 >= len(s) {
		return 0, 0, false
	}
	elemByteStart := skipWhitespace(d.data, entryOffset(s[si])+1)
	if elemByteStart >= len(d.data) || d.data[elemByteStart] == ']' {
		return 0, 0, false
	}
	if n == 0 {
		return elemByteStart, si + 1, true
	}
	if n >= 2 {
		if vs, vj, ok := d.findIndexIndexedTopology(si, n, elemByteStart); ok {
			return vs, vj, true
		}
	}
	idx := 0
	elemFirst := si + 1
	for {
		if idx == n {
			return elemByteStart, elemFirst, true
		}
		tj := d.skipValueIndex(elemFirst)
		if tj >= len(s) {
			return 0, 0, false
		}
		switch entryKind(s[tj]) {
		case kComma:
			idx++
			elemFirst = tj + 1
			elemByteStart = skipWhitespace(d.data, entryOffset(s[tj])+1)
		case kCbrack:
			return 0, 0, false
		default:
			return 0, 0, false
		}
	}
}

func (d *Document) findIndexIndexedTopology(si, n, firstByte int) (int, int, bool) {
	s := d.structurals
	data := d.data
	e0 := si + 1
	if e0 >= len(s) || entryKind(s[e0]) != kObrace {
		return 0, 0, false
	}
	t0 := d.skipValueIndex(e0)
	if t0 >= len(s) || entryKind(s[t0]) != kComma {
		return 0, 0, false
	}
	e1 := t0 + 1
	if e1 >= len(s) || entryKind(s[e1]) != kObrace {
		return 0, 0, false
	}
	t1 := d.skipValueIndex(e1)
	if t1 >= len(s) {
		return 0, 0, false
	}
	term1 := entryKind(s[t1])
	if term1 != kComma && term1 != kCbrack {
		return 0, 0, false
	}
	step := e1 - e0
	if step < 2 {
		return 0, 0, false
	}
	if t1-e1 != t0-e0 {
		return 0, 0, false
	}
	for k := 0; k < step-1; k++ {
		if entryKind(s[e0+k]) != entryKind(s[e1+k]) {
			return 0, 0, false
		}
	}
	if entryKind(s[e0+step-1]) != kComma {
		return 0, 0, false
	}
	target := e0 + n*step
	if target >= len(s) || entryKind(s[target]) != kObrace {
		return 0, 0, false
	}
	if n >= 2 {
		mid := e0 + (n/2)*step
		if mid >= len(s) || entryKind(s[mid]) != kObrace {
			return 0, 0, false
		}
		for k := 0; k < step-1 && k < 8; k++ {
			if entryKind(s[mid+k]) != entryKind(s[e0+k]) {
				return 0, 0, false
			}
		}
	}
	if n >= 8 {
		q1 := e0 + (n/4)*step
		q3 := e0 + (n*3/4)*step
		if q1 >= len(s) || q3 >= len(s) {
			return 0, 0, false
		}
		if entryKind(s[q1]) != kObrace || entryKind(s[q3]) != kObrace {
			return 0, 0, false
		}
	}
	for k := 0; k < step-1 && k < 12; k++ {
		if target+k >= len(s) || entryKind(s[target+k]) != entryKind(s[e0+k]) {
			return 0, 0, false
		}
	}
	tb := entryOffset(s[target])
	if tb >= len(data) || data[tb] != '{' {
		return 0, 0, false
	}
	if firstByte < len(data) && data[firstByte] == '{' {
		ks0, ke0, ok0 := objectFirstKey(data, firstByte)
		if ok0 {
			ksT, keT, okT := objectFirstKey(data, tb)
			if !okT || keT-ksT != ke0-ks0 {
				return 0, 0, false
			}
			for i := 0; i < ke0-ks0; i++ {
				if data[ksT+i] != data[ks0+i] {
					return 0, 0, false
				}
			}
		}
	}
	tt := d.skipValueIndex(target)
	if tt >= len(s) {
		return 0, 0, false
	}
	tk := entryKind(s[tt])
	if tk != kComma && tk != kCbrack {
		return 0, 0, false
	}
	if tt-target != t0-e0 {
		return 0, 0, false
	}
	return tb, target, true
}

// seekIndexed walks the key path over the structural index and returns the byte
// offset of the located value.
func (d *Document) seekIndexed(keys []string) (int, bool) {
	if d.oversize {
		// No structural index for oversized documents: use the unlimited
		// stateless scanner so results are correct at any size.
		return seek(d.data, keys)
	}
	if len(keys) == 0 {
		return skipWhitespace(d.data, 0), true
	}
	if len(d.structurals) == 0 {
		return 0, false // scalar document cannot be descended
	}
	si := 0
	valByteStart := skipWhitespace(d.data, 0)
	for _, key := range keys {
		if si >= len(d.structurals) {
			return 0, false
		}
		kind := entryKind(d.structurals[si])
		if n, isIdx := parseArrayIndex(key); isIdx {
			if kind != kObrack {
				return 0, false
			}
			vs, vstruct, ok := d.findIndexIndexed(si, n)
			if !ok {
				return 0, false
			}
			valByteStart = vs
			si = vstruct
		} else {
			if kind != kObrace {
				return 0, false
			}
			vs, vstruct, ok := d.findKeyIndexed(si, key)
			if !ok {
				return 0, false
			}
			valByteStart = vs
			si = vstruct
		}
	}
	return valByteStart, true
}

// Get extracts the value at the key path from the indexed document, reusing the
// shared structural index. Semantics match the package-level Get.
func (d *Document) Get(keys ...string) (value []byte, dataType ValueType, offset int, err error) {
	start, ok := d.seekIndexed(keys)
	if !ok {
		return nil, NotExist, 0, ErrKeyPathNotFound
	}
	return valueAt(d.data, start)
}

// GetBytes returns just the raw value bytes at the key path.
func (d *Document) GetBytes(keys ...string) ([]byte, error) {
	v, _, _, err := d.Get(keys...)
	return v, err
}

// GetString returns the decoded string value at the key path (allocates).
func (d *Document) GetString(keys ...string) (string, error) {
	v, vt, _, err := d.Get(keys...)
	if err != nil {
		return "", err
	}
	if vt != String {
		return "", ErrUnexpectedType
	}
	return string(unescapeInto(nil, v)), nil
}

// GetInt returns the integer value at the key path.
func (d *Document) GetInt(keys ...string) (int64, error) {
	v, vt, _, err := d.Get(keys...)
	if err != nil {
		return 0, err
	}
	if vt != Number {
		return 0, ErrUnexpectedType
	}
	return parseInt(v)
}

// GetFloat returns the floating-point value at the key path.
func (d *Document) GetFloat(keys ...string) (float64, error) {
	v, vt, _, err := d.Get(keys...)
	if err != nil {
		return 0, err
	}
	if vt != Number {
		return 0, ErrUnexpectedType
	}
	return parseFloat(v)
}

// GetBoolean returns the boolean value at the key path.
func (d *Document) GetBoolean(keys ...string) (bool, error) {
	v, vt, _, err := d.Get(keys...)
	if err != nil {
		return false, err
	}
	if vt != Boolean {
		return false, ErrUnexpectedType
	}
	return parseBool(v)
}

// Exists reports whether the key path resolves to a value in the document.
func (d *Document) Exists(keys ...string) bool {
	_, ok := d.seekIndexed(keys)
	return ok
}
