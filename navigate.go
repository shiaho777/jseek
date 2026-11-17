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
	if n != len(key) {
		return false
	}
	// A SWAR wide-compare variant was prototyped here and measured slower
	// than this scalar loop even on 90+ byte keys: the target-key loads are
	// unaligned and the per-call safety check outweighs the word-at-a-time
	// gain. (scanKey's fused scan+compare wide path is a different shape and
	// does win — see scanKeyWide.)
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
// This fuses what would otherwise be two passes (skipString + keyMatches).
// Targets shorter than 16 bytes keep the tight scalar compare with early
// mismatch bail: the wide path's per-call setup does not amortize below that
// (measured). Longer targets compare eight bytes per step with SWAR: a key
// whose bytes are known to be ordinary (no quote, backslash, or control byte)
// can only end at a quote or backslash in the document, so one register mask
// locates the next candidate terminator while a single word comparison
// validates all eight content bytes at once. On encountering an escape it
// defers to the exact escape-aware comparison.
func scanKey(data []byte, i int, key string) (keyEnd int, matched bool, ok bool) {
	if len(key) >= 16 && keyIsWideSafe(key) {
		return scanKeyWide(data, i, key)
	}
	return scanKeyScalar(data, i, key)
}

// keyIsWideSafe reports whether every byte of key is an ordinary content byte
// (>= 0x20, not '"' or '\\'). For such targets the document key can only
// terminate at a quote or backslash, which lets scanKeyWide skip the
// per-byte terminator checks inside the bulk loop. Scalar with early bail:
// keys with an exceptional byte usually reveal it in the first few bytes.
func keyIsWideSafe(key string) bool {
	for j := 0; j < len(key); j++ {
		c := key[j]
		if c < 0x20 || c == '"' || c == '\\' {
			return false
		}
	}
	return true
}

func scanKeyWide(data []byte, i int, key string) (keyEnd int, matched bool, ok bool) {
	n := len(data)
	p := i + 1
	klen := len(key)
	quote := broadcast('"')
	back := broadcast('\\')
	for klen-(p-i-1) >= 8 && p+8 <= n {
		v := load64(data, p)
		// Exact masks: the classic trick false-positives on 0x80 bytes
		// (common in UTF-8 keys), which would truncate the scan early.
		if zeroByteMaskExact(v^quote)|zeroByteMaskExact(v^back) != 0 {
			break
		}
		if v != load64str(key, p-i-1) {
			// Content diverges: the key cannot match, but its extent must
			// still be scanned so navigation can skip the value.
			j := indexQuoteOrBackslash(data, p)
			if j < 0 {
				return n, false, false
			}
			if data[j] == '"' {
				return j + 1, false, true
			}
			// Backslash at j: resume the body scan past the escape pair.
			ke, sok := skipStringBody(data, j+2)
			return ke, false, sok
		}
		p += 8
	}
	// Scalar tail: fewer than 8 target bytes remain, or a quote/backslash
	// landed inside the next word. Same semantics as the short-key path.
	ki := p - i - 1
	matched = true
	for p < n {
		c := data[p]
		if c == '"' {
			return p + 1, matched && ki == klen, true
		}
		if c == '\\' {
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
	return p, false, false
}

func scanKeyScalar(data []byte, i int, key string) (keyEnd int, matched bool, ok bool) {
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
	return p, false, false
}

// findKey locates the value for key within the object beginning at data[oi]
// (data[oi] must be '{'). It returns the index of the start of the value (past
// whitespace) and true if found.
func findKey(data []byte, oi int, key string) (int, bool) {
	n := len(data)
	i := oi + 1
	if i >= n {
		return i, false
	}
	c := data[i]
	if c == ' ' || c == 9 || c == 10 || c == 13 {
		i = skipWhitespace(data, i)
		if i >= n {
			return i, false
		}
		c = data[i]
	}
	if c == '}' {
		return i, false
	}
	for i < n {
		if data[i] != '"' {
			return i, false
		}
		ke, match, ok := scanKey(data, i, key)
		if !ok {
			return ke, false
		}
		i = ke
		if i >= n {
			return i, false
		}
		if data[i] == ':' {
			i++
		} else {
			i = skipWhitespace(data, i)
			if i >= n || data[i] != ':' {
				return i, false
			}
			i++
		}
		if i < n {
			c = data[i]
			if c == ' ' || c == 9 || c == 10 || c == 13 {
				i = skipWhitespace(data, i)
			}
		}
		if match {
			return i, true
		}
		if i, ok = skipValue(data, i); !ok {
			return i, false
		}
		if i >= n {
			return i, false
		}
		c = data[i]
		if c == ',' {
			i++
			if i < n {
				c = data[i]
				if c == ' ' || c == 9 || c == 10 || c == 13 {
					i = skipWhitespace(data, i)
				}
			}
			continue
		}
		if c == ' ' || c == 9 || c == 10 || c == 13 {
			i = skipWhitespace(data, i)
			if i >= n {
				return i, false
			}
			switch data[i] {
			case ',':
				i++
				if i < n {
					c = data[i]
					if c == ' ' || c == 9 || c == 10 || c == 13 {
						i = skipWhitespace(data, i)
					}
				}
				continue
			case '}':
				return i, false
			default:
				return i, false
			}
		}
		if c == '}' {
			return i, false
		}
		return i, false
	}
	return i, false
}

func findIndexGeneric(data []byte, ai int, n int) (int, bool) {
	nlen := len(data)
	i := ai + 1
	if i >= nlen {
		return i, false
	}
	c := data[i]
	if c == ' ' || c == 9 || c == 10 || c == 13 {
		i = skipWhitespace(data, i)
		if i >= nlen {
			return i, false
		}
		c = data[i]
	}
	if c == ']' {
		return i, false
	}
	if c == '{' {
		return findIndexObjectStride(data, i, n, nlen)
	}
	return findIndexSlow(data, i, n, nlen)
}

func objectFirstKey(data []byte, obj int) (start, end int, ok bool) {
	nlen := len(data)
	if obj >= nlen || data[obj] != '{' {
		return 0, 0, false
	}
	j := obj + 1
	if j < nlen && (data[j] == ' ' || data[j] == 9 || data[j] == 10 || data[j] == 13) {
		j = skipWhitespace(data, j)
	}
	if j >= nlen || data[j] != '"' {
		return 0, 0, false
	}
	start = j + 1
	j = start
	for j < nlen {
		c := data[j]
		if c == '"' {
			return start, j, true
		}
		if c == '\\' {
			return 0, 0, false
		}
		j++
	}
	return 0, 0, false
}

func firstKeyEqual(data []byte, obj int, ks, ke int) bool {
	nlen := len(data)
	if obj >= nlen || data[obj] != '{' {
		return false
	}
	klen := ke - ks
	p := obj + 1
	if p >= nlen {
		return false
	}
	if data[p] != '"' {
		if data[p] == ' ' || data[p] == 9 || data[p] == 10 || data[p] == 13 {
			p = skipWhitespace(data, p)
			if p >= nlen || data[p] != '"' {
				return false
			}
		} else {
			return false
		}
	}
	p++
	if p+klen >= nlen || data[p+klen] != '"' {
		return false
	}
	for i := 0; i < klen; i++ {
		if data[p+i] != data[ks+i] {
			return false
		}
	}
	return true
}

func strideObject(data []byte, i, lastLen, nlen int, ks, ke int, haveKey bool) (int, bool) {
	if lastLen < 2 {
		return i, false
	}
	end := i + lastLen
	if end+1 >= nlen || data[i] != '{' || data[end-1] != '}' {
		return i, false
	}
	if data[end] == ',' {
		ni := end + 1
		if data[ni] == '{' {
			if haveKey && !firstKeyEqual(data, ni, ks, ke) {
				return i, false
			}
			return ni, true
		}
		if data[ni] == ' ' || data[ni] == 9 || data[ni] == 10 || data[ni] == 13 {
			ni = skipWhitespace(data, ni)
			if ni < nlen && data[ni] == '{' {
				if haveKey && !firstKeyEqual(data, ni, ks, ke) {
					return i, false
				}
				return ni, true
			}
		}
		return i, false
	}
	if data[end] == ' ' || data[end] == 9 || data[end] == 10 || data[end] == 13 {
		ni := skipWhitespace(data, end)
		if ni >= nlen || data[ni] != ',' {
			return i, false
		}
		ni++
		if ni < nlen && (data[ni] == ' ' || data[ni] == 9 || data[ni] == 10 || data[ni] == 13) {
			ni = skipWhitespace(data, ni)
		}
		if ni < nlen && data[ni] == '{' {
			if haveKey && !firstKeyEqual(data, ni, ks, ke) {
				return i, false
			}
			return ni, true
		}
	}
	return i, false
}

func objectLenMinified(data []byte, pos, lastLen, nlen int) bool {
	end := pos + lastLen
	if end > nlen || lastLen < 2 || data[pos] != '{' || data[end-1] != '}' {
		return false
	}
	if end == nlen {
		return true
	}
	c := data[end]
	return c == ',' || c == ']' || c == ' ' || c == 9 || c == 10 || c == 13
}

func tryDirectJump(data []byte, i, remain, lastLen, nlen int, ks, ke int, haveKey bool) (int, bool) {
	if remain <= 0 || lastLen < 2 {
		return i, false
	}
	step := lastLen + 1
	probe := i + remain*step
	if probe >= nlen || data[i] != '{' || data[probe] != '{' {
		return i, false
	}
	if !objectLenMinified(data, i, lastLen, nlen) {
		return i, false
	}
	if haveKey && !firstKeyEqual(data, probe, ks, ke) {
		return i, false
	}
	if remain >= 1 {
		prev := i + (remain-1)*step
		if prev != i {
			if data[prev] != '{' || !objectLenMinified(data, prev, lastLen, nlen) {
				return i, false
			}
		}
	}
	if remain >= 2 {
		mid := i + (remain/2)*step
		if data[mid] != '{' || !objectLenMinified(data, mid, lastLen, nlen) {
			return i, false
		}
		if haveKey && !firstKeyEqual(data, mid, ks, ke) {
			return i, false
		}
	}
	if remain >= 8 {
		q1 := i + (remain/4)*step
		q3 := i + (remain*3/4)*step
		if data[q1] != '{' || !objectLenMinified(data, q1, lastLen, nlen) {
			return i, false
		}
		if data[q3] != '{' || !objectLenMinified(data, q3, lastLen, nlen) {
			return i, false
		}
	}
	if remain >= 32 {
		for k := 8; k < remain; k += 8 {
			pos := i + k*step
			if pos >= probe {
				break
			}
			if data[pos] != '{' || data[pos+lastLen-1] != '}' || data[pos+lastLen] != ',' {
				return i, false
			}
		}
	}
	end, ok := skipContainer(data, probe)
	if !ok || end-probe != lastLen {
		return i, false
	}
	return probe, true
}

func strideObjectsMinified(data []byte, i, lastLen, count, nlen int, ks, ke int, haveKey bool) (int, int, bool) {
	if lastLen < 2 || count <= 0 || i+lastLen+1 >= nlen {
		return i, 0, false
	}
	if data[i] != '{' {
		return i, 0, false
	}
	if count >= 4 {
		if ni, ok := tryDirectJump(data, i, count, lastLen, nlen, ks, ke, haveKey); ok {
			return ni, count, true
		}
	}
	pos := i
	for j := 0; j < count; j++ {
		end := pos + lastLen
		if end+1 >= nlen || data[end-1] != '}' || data[end] != ',' || data[end+1] != '{' {
			if j == 0 {
				return i, 0, false
			}
			if haveKey && !firstKeyEqual(data, pos, ks, ke) {
				return i, 0, false
			}
			return pos, j, true
		}
		pos = end + 1
	}
	if haveKey && !firstKeyEqual(data, pos, ks, ke) {
		return i, 0, false
	}
	end, ok := skipContainer(data, pos)
	if !ok || end-pos != lastLen {
		return i, 0, false
	}
	return pos, count, true
}

func findIndexObjectStride(data []byte, i, n, nlen int) (int, bool) {
	if n == 0 {
		return i, true
	}
	lastLen := 0
	ks, ke := 0, 0
	haveKey := false
	strideOK := false
	strideConfirmed := false
	minified := false
	for idx := 0; idx < n; {
		if i >= nlen || data[i] != '{' {
			return findIndexSlow(data, i, n-idx, nlen)
		}
		if strideConfirmed && lastLen > 0 {
			remain := n - idx
			if minified {
				if ni, jumped, ok := strideObjectsMinified(data, i, lastLen, remain, nlen, ks, ke, haveKey); ok {
					i = ni
					idx += jumped
					continue
				}
			}
			if ni, ok := strideObject(data, i, lastLen, nlen, ks, ke, haveKey); ok {
				i = ni
				idx++
				continue
			}
			strideConfirmed = false
		}
		start := i
		var ok bool
		i, ok = skipContainer(data, i)
		if !ok {
			return i, false
		}
		curLen := i - start
		if lastLen > 0 && strideOK {
			if curLen == lastLen {
				strideConfirmed = true
			} else {
				strideConfirmed = false
				lastLen = curLen
				haveKey = false
				if s, e, kok := objectFirstKey(data, start); kok {
					ks, ke = s, e
					haveKey = true
				}
				if i < nlen && data[i] == ',' {
					minified = true
				} else {
					minified = false
				}
			}
		} else {
			lastLen = curLen
			if !haveKey {
				if s, e, kok := objectFirstKey(data, start); kok {
					ks, ke = s, e
					haveKey = true
				}
			}
			if !strideOK && lastLen > 0 {
				strideOK = true
			}
		}
		if i >= nlen {
			return i, false
		}
		c := data[i]
		if c == ',' {
			i++
			if i < nlen && (data[i] == ' ' || data[i] == 9 || data[i] == 10 || data[i] == 13) {
				i = skipWhitespace(data, i)
				minified = false
			} else if strideOK {
				minified = true
			}
			idx++
			if minified && strideConfirmed && lastLen > 0 {
				remain := n - idx
				if remain >= 4 {
					if ni, ok := tryDirectJump(data, i, remain, lastLen, nlen, ks, ke, haveKey); ok {
						return ni, true
					}
				}
			}
			continue
		}
		if c == ' ' || c == 9 || c == 10 || c == 13 {
			minified = false
			i = skipWhitespace(data, i)
			if i >= nlen {
				return i, false
			}
			if data[i] == ',' {
				i++
				if i < nlen && (data[i] == ' ' || data[i] == 9 || data[i] == 10 || data[i] == 13) {
					i = skipWhitespace(data, i)
				}
				idx++
				continue
			}
		}
		return i, false
	}
	return i, true
}

func findIndexSlow(data []byte, i int, n int, nlen int) (int, bool) {
	idx := 0
	for i < nlen {
		if idx == n {
			return i, true
		}
		var ok bool
		c := data[i]
		if c == '{' || c == '[' {
			i, ok = skipContainer(data, i)
			if !ok {
				return i, false
			}
		} else {
			i, ok = skipValue(data, i)
			if !ok {
				return i, false
			}
		}
		if i >= nlen {
			return i, false
		}
		c = data[i]
		if c == ',' {
			i++
			idx++
			if i < nlen && (data[i] == ' ' || data[i] == 9 || data[i] == 10 || data[i] == 13) {
				i = skipWhitespace(data, i)
			}
			continue
		}
		if c == ']' {
			return i, false
		}
		if c == ' ' || c == 9 || c == 10 || c == 13 {
			i = skipWhitespace(data, i)
			if i >= nlen {
				return i, false
			}
			c = data[i]
			if c == ',' {
				i++
				idx++
				if i < nlen && (data[i] == ' ' || data[i] == 9 || data[i] == 10 || data[i] == 13) {
					i = skipWhitespace(data, i)
				}
				continue
			}
			if c == ']' {
				return i, false
			}
			return i, false
		}
		return i, false
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
