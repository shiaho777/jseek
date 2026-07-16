package jseek

func expandStructuralTemplate(out, tpl []uint32, base int) []uint32 {
	for _, e := range tpl {
		off := base + entryOffset(e)
		if off < base {
			continue
		}
		out = append(out, packEntry(entryKind(e), uint32(off)))
	}
	return out
}

func structuralTemplateFrom(out []uint32, from int, base int, tpl []uint32) []uint32 {
	n := len(out) - from
	if cap(tpl) < n {
		tpl = make([]uint32, n)
	} else {
		tpl = tpl[:n]
	}
	for i := 0; i < n; i++ {
		e := out[from+i]
		tpl[i] = packEntry(entryKind(e), uint32(entryOffset(e)-base))
	}
	return tpl
}

func templateAnchorOK(data []byte, base, objLen int, tpl []uint32) bool {
	if objLen < 2 || base < 0 || base+objLen > len(data) {
		return false
	}
	if data[base] != '{' || data[base+objLen-1] != '}' {
		return false
	}
	if len(tpl) == 0 || entryKind(tpl[0]) != kObrace || entryOffset(tpl[0]) != 0 {
		return false
	}
	last := tpl[len(tpl)-1]
	if entryKind(last) != kCbrace || entryOffset(last) != objLen-1 {
		return false
	}
	limit := 8
	if len(tpl) < limit {
		limit = len(tpl)
	}
	for i := 1; i < limit; i++ {
		e := tpl[i]
		off := base + entryOffset(e)
		if off >= base+objLen {
			return false
		}
		if structuralKind[data[off]] != entryKind(e) {
			return false
		}
	}
	if len(tpl) > 10 {
		mid := tpl[len(tpl)/2]
		off := base + entryOffset(mid)
		if off >= base+objLen || structuralKind[data[off]] != entryKind(mid) {
			return false
		}
	}
	return true
}

func indexStructuralsBounded(data []byte, lo, hi int, out []uint32) ([]uint32, bool) {
	if lo < 0 || hi > len(data) || lo >= hi {
		return out, false
	}
	i := lo
	for i < hi {
		c := data[i]
		sk := structuralKind[c]
		if sk != 0 {
			if sk == kQuote {
				out = append(out, packEntry(kQuote, uint32(i)))
				if end := skipStringShort(data, i); end >= 0 {
					if end > hi {
						return out, false
					}
					i = end
					continue
				}
				end, ok := skipString(data, i)
				if !ok || end > hi {
					return out, false
				}
				i = end
				continue
			}
			out = append(out, packEntry(sk, uint32(i)))
			i++
			continue
		}
		if c == ' ' || c == 9 || c == 10 || c == 13 {
			i = indexSkipWhitespace(data, i)
			if i > hi {
				i = hi
			}
			continue
		}
		j := indexStructuralOrQuote(data, i+1)
		if j < 0 || j >= hi {
			return out, true
		}
		i = j
	}
	return out, true
}

func countEqualMinifiedRun(data []byte, start, objLen, n int) int {
	if objLen < 2 || start >= n || data[start] != '{' {
		return 0
	}
	step := objLen + 1
	count := 0
	pos := start
	for pos+objLen <= n && data[pos] == '{' && data[pos+objLen-1] == '}' {
		b := byte(0)
		if pos+objLen < n {
			b = data[pos+objLen]
		}
		if b != ',' && b != ']' {
			break
		}
		count++
		if b == ']' {
			break
		}
		pos += step
		if count >= 4096 {
			break
		}
	}
	return count
}

func steBulkExpand(data []byte, start, objLen, count int, tpl []uint32, out []uint32) ([]uint32, int, bool) {
	if count <= 0 || objLen < 2 {
		return out, start, false
	}
	step := objLen + 1
	last := start + (count-1)*step
	if last+objLen > len(data) || data[start] != '{' || data[last] != '{' {
		return out, start, false
	}
	if !templateAnchorOK(data, start, objLen, tpl) || !templateAnchorOK(data, last, objLen, tpl) {
		return out, start, false
	}
	if count >= 4 {
		mid := start + (count/2)*step
		if data[mid] != '{' || !templateAnchorOK(data, mid, objLen, tpl) {
			return out, start, false
		}
	}
	if count >= 8 {
		q1 := start + (count/4)*step
		q3 := start + (count*3/4)*step
		if data[q1] != '{' || data[q3] != '{' {
			return out, start, false
		}
		if !templateAnchorOK(data, q1, objLen, tpl) || !templateAnchorOK(data, q3, objLen, tpl) {
			return out, start, false
		}
	}
	pos := start
	for k := 0; k < count; k++ {
		if data[pos] != '{' || data[pos+objLen-1] != '}' {
			return out, start, false
		}
		out = expandStructuralTemplate(out, tpl, pos)
		end := pos + objLen
		if k == count-1 {
			return out, end, true
		}
		if end >= len(data) || data[end] != ',' {
			return out, start, false
		}
		out = append(out, packEntry(kComma, uint32(end)))
		pos = end + 1
	}
	return out, start, false
}


func indexObjectArraySTE(data []byte, i int, out []uint32) ([]uint32, int, bool) {
	n := len(data)
	if i >= n || data[i] != '{' {
		return out, i, false
	}
	mark := len(out)
	var steTpl [128]uint32
	tpl := steTpl[:0]
	objLen := -1
	minified := false
	confirmed := false
	seeded := false
	count := 0
	steGuard := 0
	for i < n {
		steGuard++
		if steGuard > n+8 {
			return out[:mark], i, false
		}
		c := data[i]
		if c == ' ' || c == 9 || c == 10 || c == 13 {
			i = indexSkipWhitespace(data, i)
			if i >= n {
				return out[:mark], i, false
			}
			c = data[i]
		}
		if c == ']' {
			if count == 0 {
				return out[:mark], i, false
			}
			return out, i, true
		}
		if c != '{' {
			return out[:mark], i, false
		}
		start := i
		usedTemplate := false
		if confirmed && objLen > 0 && minified && start+objLen <= n && data[start+objLen-1] == '}' {
			run := countEqualMinifiedRun(data, start, objLen, n)
			if run >= 4 && templateAnchorOK(data, start, objLen, tpl) {
				var ok bool
				var end int
				out, end, ok = steBulkExpand(data, start, objLen, run, tpl, out)
				if ok {
					count += run
					i = end
					usedTemplate = true
				}
			} else if run >= 1 && templateAnchorOK(data, start, objLen, tpl) {
				b := byte(0)
				if start+objLen < n {
					b = data[start+objLen]
				}
				if b == ',' || b == ']' {
					out = expandStructuralTemplate(out, tpl, start)
					count++
					i = start + objLen
					usedTemplate = true
				}
			}
		}
		if !usedTemplate {
			end, ok := skipContainer(data, start)
			if !ok {
				return out[:mark], i, false
			}
			curLen := end - start
			if seeded && curLen == objLen && templateAnchorOK(data, start, objLen, tpl) {
				out = expandStructuralTemplate(out, tpl, start)
				confirmed = true
			} else {
				from := len(out)
				out, ok = indexStructuralsBounded(data, start, end, out)
				if !ok {
					return out[:mark], i, false
				}
				tpl = structuralTemplateFrom(out, from, start, tpl)
				if len(tpl) == 0 {
					return out[:mark], i, false
				}
				objLen = curLen
				seeded = true
				confirmed = false
			}
			count++
			i = end
		}
		if i < n && (data[i] == ' ' || data[i] == 9 || data[i] == 10 || data[i] == 13) {
			minified = false
			confirmed = false
			i = indexSkipWhitespace(data, i)
		}
		if i >= n {
			return out[:mark], i, false
		}
		if data[i] == ',' {
			out = append(out, packEntry(kComma, uint32(i)))
			i++
			if i < n && (data[i] == ' ' || data[i] == 9 || data[i] == 10 || data[i] == 13) {
				minified = false
				confirmed = false
				i = indexSkipWhitespace(data, i)
			} else if seeded {
				minified = true
			}
			continue
		}
		if data[i] == ']' {
			return out, i, true
		}
		return out[:mark], i, false
	}
	return out[:mark], i, false
}
