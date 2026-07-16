package jseek

func findKeys(data []byte, oi int, keys []string, dst []int) int {
	nlen := len(data)
	nk := len(keys)
	if nk == 0 {
		return 0
	}
	limit := nk
	if limit > len(dst) {
		limit = len(dst)
	}
	for i := 0; i < limit; i++ {
		dst[i] = -1
	}
	found := 0
	i := oi + 1
	if i >= nlen {
		return 0
	}
	c := data[i]
	if c == ' ' || c == 9 || c == 10 || c == 13 {
		i = skipWhitespace(data, i)
		if i >= nlen {
			return 0
		}
		c = data[i]
	}
	if c == '}' {
		return 0
	}
	for i < nlen && found < limit {
		if data[i] != '"' {
			return found
		}
		ke, which, ok := scanKeyWhich(data, i, keys, dst)
		if !ok {
			return found
		}
		i = ke
		if i >= nlen {
			return found
		}
		if data[i] == ':' {
			i++
		} else {
			i = skipWhitespace(data, i)
			if i >= nlen || data[i] != ':' {
				return found
			}
			i++
		}
		if i < nlen {
			c = data[i]
			if c == ' ' || c == 9 || c == 10 || c == 13 {
				i = skipWhitespace(data, i)
			}
		}
		if which >= 0 && which < limit && dst[which] == -1 {
			dst[which] = i
			found++
			if found == limit {
				return found
			}
		}
		if i, ok = skipValue(data, i); !ok {
			return found
		}
		if i >= nlen {
			return found
		}
		c = data[i]
		if c == ',' {
			i++
			if i < nlen {
				c = data[i]
				if c == ' ' || c == 9 || c == 10 || c == 13 {
					i = skipWhitespace(data, i)
				}
			}
			continue
		}
		if c == ' ' || c == 9 || c == 10 || c == 13 {
			i = skipWhitespace(data, i)
			if i >= nlen {
				return found
			}
			if data[i] == ',' {
				i++
				if i < nlen {
					c = data[i]
					if c == ' ' || c == 9 || c == 10 || c == 13 {
						i = skipWhitespace(data, i)
					}
				}
				continue
			}
			return found
		}
		if c == '}' {
			return found
		}
		return found
	}
	return found
}

func scanKeyWhich(data []byte, i int, keys []string, dst []int) (keyEnd int, which int, ok bool) {
	n := len(data)
	const maxK = 16
	var active [maxK]int16
	na := 0
	for k := 0; k < len(keys) && na < maxK; k++ {
		if k < len(dst) && dst[k] == -1 {
			active[na] = int16(k)
			na++
		}
	}
	if na == 0 {
		ke, sok := skipString(data, i)
		return ke, -1, sok
	}
	p := i + 1
	pos := 0
	for p < n {
		c := data[p]
		if c == '"' {
			for a := 0; a < na; a++ {
				k := int(active[a])
				if pos == len(keys[k]) {
					return p + 1, k, true
				}
			}
			return p + 1, -1, true
		}
		if c == '\\' {
			ke, sok := skipString(data, i)
			if !sok {
				return ke, -1, false
			}
			raw := data[i+1 : ke-1]
			for a := 0; a < na; a++ {
				k := int(active[a])
				if escapedEquals(raw, keys[k]) {
					return ke, k, true
				}
			}
			return ke, -1, true
		}
		w := 0
		for a := 0; a < na; a++ {
			k := int(active[a])
			if pos < len(keys[k]) && keys[k][pos] == c {
				active[w] = active[a]
				w++
			}
		}
		na = w
		pos++
		p++
	}
	return p, -1, false
}

func GetFields(data []byte, path []string, keys ...string) ([]Result, error) {
	start, ok := seek(data, path)
	if !ok {
		return nil, ErrKeyPathNotFound
	}
	if start >= len(data) || data[start] != '{' {
		return nil, ErrUnexpectedType
	}
	out := make([]Result, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	dst := make([]int, len(keys))
	findKeys(data, start, keys, dst)
	for i := range keys {
		if dst[i] < 0 {
			out[i] = Result{Type: NotExist}
			continue
		}
		v, vt, _, err := valueAt(data, dst[i])
		if err != nil {
			out[i] = Result{Type: NotExist}
			continue
		}
		out[i] = Result{Raw: v, Type: vt}
	}
	return out, nil
}

func EachField(data []byte, path []string, keys []string, cb func(idx int, value []byte, dataType ValueType, err error)) {
	var offs [16]int
	o := offs[:0]
	if len(keys) > len(offs) {
		o = make([]int, len(keys))
	} else {
		o = offs[:len(keys)]
	}
	EachFieldInto(data, path, keys, o, cb)
}

func EachFieldInto(data []byte, path []string, keys []string, offs []int, cb func(idx int, value []byte, dataType ValueType, err error)) {
	start, ok := seek(data, path)
	if !ok {
		for i := range keys {
			cb(i, nil, NotExist, ErrKeyPathNotFound)
		}
		return
	}
	if start >= len(data) || data[start] != '{' {
		for i := range keys {
			cb(i, nil, NotExist, ErrUnexpectedType)
		}
		return
	}
	if len(keys) == 0 {
		return
	}
	if len(offs) < len(keys) {
		offs = make([]int, len(keys))
	} else {
		offs = offs[:len(keys)]
	}
	findKeys(data, start, keys, offs)
	for i := range keys {
		if offs[i] < 0 {
			cb(i, nil, NotExist, ErrKeyPathNotFound)
			continue
		}
		v, vt, _, err := valueAt(data, offs[i])
		cb(i, v, vt, err)
	}
}

func walkObjectFields(data []byte, oi int, keys []string, dst []int) (end int, ok bool) {
	nlen := len(data)
	nk := len(keys)
	if nk == 0 {
		return skipContainer(data, oi)
	}
	limit := nk
	if limit > len(dst) {
		limit = len(dst)
	}
	for i := 0; i < limit; i++ {
		dst[i] = -1
	}
	found := 0
	matching := true
	i := oi + 1
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
	if c == '}' {
		return i + 1, true
	}
	for i < nlen {
		if data[i] != '"' {
			return i, false
		}
		var ke int
		var which int
		var sok bool
		if matching {
			ke, which, sok = scanKeyWhich(data, i, keys, dst)
		} else {
			ke, sok = skipString(data, i)
			which = -1
		}
		if !sok {
			return ke, false
		}
		i = ke
		if i >= nlen {
			return i, false
		}
		if data[i] == ':' {
			i++
		} else {
			i = skipWhitespace(data, i)
			if i >= nlen || data[i] != ':' {
				return i, false
			}
			i++
		}
		if i < nlen {
			c = data[i]
			if c == ' ' || c == 9 || c == 10 || c == 13 {
				i = skipWhitespace(data, i)
			}
		}
		if matching && which >= 0 && which < limit && dst[which] == -1 {
			dst[which] = i
			found++
			if found == limit {
				matching = false
			}
		}
		if i, ok = skipValue(data, i); !ok {
			return i, false
		}
		if i >= nlen {
			return i, false
		}
		c = data[i]
		if c == ',' {
			i++
			if i < nlen {
				c = data[i]
				if c == ' ' || c == 9 || c == 10 || c == 13 {
					i = skipWhitespace(data, i)
				}
			}
			continue
		}
		if c == ' ' || c == 9 || c == 10 || c == 13 {
			i = skipWhitespace(data, i)
			if i >= nlen {
				return i, false
			}
			if data[i] == ',' {
				i++
				if i < nlen {
					c = data[i]
					if c == ' ' || c == 9 || c == 10 || c == 13 {
						i = skipWhitespace(data, i)
					}
				}
				continue
			}
			if data[i] == '}' {
				return i + 1, true
			}
			return i, false
		}
		if c == '}' {
			return i + 1, true
		}
		return i, false
	}
	return i, false
}

func EachArrayFields(data []byte, path []string, keys []string, cb func(elem, key int, value []byte, dataType ValueType, err error) bool) error {
	start, ok := seek(data, path)
	if !ok {
		return ErrKeyPathNotFound
	}
	if start >= len(data) || data[start] != '[' {
		return ErrUnexpectedType
	}
	nlen := len(data)
	i := skipWhitespace(data, start+1)
	if i < nlen && data[i] == ']' {
		return nil
	}
	nk := len(keys)
	var stack [16]int
	var dst []int
	if nk <= len(stack) {
		dst = stack[:nk]
	} else {
		dst = make([]int, nk)
	}
	elem := 0
	for i < nlen {
		if data[i] == '{' {
			end, wok := walkObjectFields(data, i, keys, dst)
			if !wok {
				return ErrMalformedJSON
			}
			for k := 0; k < nk; k++ {
				if dst[k] < 0 {
					if !cb(elem, k, nil, NotExist, ErrKeyPathNotFound) {
						return nil
					}
					continue
				}
				v, vt, _, err := valueAt(data, dst[k])
				if !cb(elem, k, v, vt, err) {
					return nil
				}
			}
			i = end
		} else {
			vs, ve, vt, vok := valueBounds(data, i)
			if !vok {
				return ErrMalformedJSON
			}
			_ = vs
			if vt == String {
				i = ve + 1
			} else {
				i = ve
			}
		}
		i = skipWhitespace(data, i)
		if i >= nlen {
			return ErrMalformedJSON
		}
		switch data[i] {
		case ',':
			i = skipWhitespace(data, i+1)
			elem++
		case ']':
			return nil
		default:
			return ErrMalformedJSON
		}
	}
	return ErrMalformedJSON
}
