package jseek

// Index/tape-aware multi-path matching. EachDoc runs a compiled Paths set over
// an indexed Document, navigating the structural index instead of re-scanning
// raw bytes. When the Document carries a skip tape, stepping over an unwanted
// object member or array element is O(1) rather than O(subtree size), which is
// the dominant cost for multi-path reads on large documents (confirmed by
// profiling: ~94% of EachKey time was in byte-level skipValue).
//
// Semantics are identical to Paths.Each: same first-occurrence-wins duplicate
// handling, same value/quote rules, same callback contract. This equivalence is
// enforced by FuzzEachDocMatchesEachKey.

// EachDoc extracts all compiled paths from an indexed document in a single
// navigation, invoking cb once per path found. It performs no allocation beyond
// what the callback does.
func (p *Paths) EachDoc(d *Document, cb func(idx int, value []byte, dataType ValueType, err error)) {
	if d.oversize {
		// Oversized document has no index: run the stateless multi-path matcher.
		p.Each(d.data, cb)
		return
	}
	if len(p.nodes) == 0 || len(d.structurals) == 0 {
		// No structurals: a scalar document. Only the root terminal (empty
		// path) could match, handled by matchDoc at si=-1 below when present.
		p.matchDocScalar(d, cb)
		return
	}
	p.matchDoc(d, skipWhitespace(d.data, 0), 0, 0, cb)
}

// matchDocScalar handles the degenerate case of a document with no structural
// characters (a bare scalar like 42 or "x"): only an empty requested path can
// match it.
func (p *Paths) matchDocScalar(d *Document, cb func(int, []byte, ValueType, error)) {
	node := &p.nodes[0]
	if len(node.terminal) == 0 {
		return
	}
	start := skipWhitespace(d.data, 0)
	vs, ve, vt, ok := valueBounds(d.data, start)
	if !ok {
		for _, idx := range node.terminal {
			cb(idx, nil, NotExist, ErrMalformedJSON)
		}
		return
	}
	val := d.data[vs:ve]
	for _, idx := range node.terminal {
		cb(idx, val, vt, nil)
	}
}

// matchDoc is the recursive matcher. byteStart is the byte offset of the value;
// si is the position in the structural index of that value's first structural
// (or -1 if the value is a scalar with no structural); ni is the trie node.
func (p *Paths) matchDoc(d *Document, byteStart, si, ni int, cb func(int, []byte, ValueType, error)) {
	node := &p.nodes[ni]
	if len(node.terminal) > 0 {
		vs, ve, vt, ok := valueBounds(d.data, byteStart)
		if !ok {
			for _, idx := range node.terminal {
				cb(idx, nil, NotExist, ErrMalformedJSON)
			}
		} else {
			val := d.data[vs:ve]
			for _, idx := range node.terminal {
				cb(idx, val, vt, nil)
			}
		}
	}
	if len(node.edges) == 0 || si < 0 || si >= len(d.structurals) {
		return
	}
	switch entryKind(d.structurals[si]) {
	case kObrace:
		p.matchDocObject(d, si, ni, cb)
	case kObrack:
		p.matchDocArray(d, si, ni, cb)
	}
}

func (p *Paths) matchDocObject(d *Document, si, ni int, cb func(int, []byte, ValueType, error)) {
	s := d.structurals
	// Empty object check from bytes (a scalar-only object still has } as next
	// structural, but here an object's members always begin with a key quote).
	j := si + 1
	if j >= len(s) || entryKind(s[j]) == kCbrace {
		return
	}
	var used uint64
	edges := p.nodes[ni].edges
	for j < len(s) {
		if entryKind(s[j]) != kQuote {
			return
		}
		keyOpen := entryOffset(s[j])
		keyEnd, ok := skipString(d.data, keyOpen)
		if !ok {
			return
		}
		cj := j + 1
		if cj >= len(s) || entryKind(s[cj]) != kColon {
			return
		}
		// Value begins after the colon.
		valByteStart := skipWhitespace(d.data, entryOffset(s[cj])+1)
		valSi := cj + 1 // structural position of the value's first structural

		// Does any requested edge want this key?
		for ei := range edges {
			e := &edges[ei]
			if e.isIndex {
				continue
			}
			if ei < 64 && used&(1<<uint(ei)) != 0 {
				continue
			}
			if keyMatches(d.data, keyOpen+1, keyEnd-1, e.key) {
				if ei < 64 {
					used |= 1 << uint(ei)
				}
				p.matchDoc(d, valByteStart, valSi, e.child, cb)
				break
			}
		}

		// Advance past this member's value using the (possibly O(1)) index skip.
		tj := d.skipValueIndex(valSi)
		if tj >= len(s) {
			return
		}
		switch entryKind(s[tj]) {
		case kComma:
			j = tj + 1
		case kCbrace:
			return
		default:
			return
		}
	}
}

func (p *Paths) matchDocArray(d *Document, si, ni int, cb func(int, []byte, ValueType, error)) {
	s := d.structurals
	// Determine the highest index requested, to stop early.
	maxWanted := -1
	hasIndexEdge := false
	edges := p.nodes[ni].edges
	for ei := range edges {
		if edges[ei].isIndex {
			hasIndexEdge = true
			if edges[ei].index > maxWanted {
				maxWanted = edges[ei].index
			}
		}
	}
	if !hasIndexEdge {
		return
	}
	// Empty array?
	if b := skipWhitespace(d.data, entryOffset(s[si])+1); b < len(d.data) && d.data[b] == ']' {
		return
	}
	idx := 0
	elemSi := si + 1
	elemByteStart := skipWhitespace(d.data, entryOffset(s[si])+1)
	for {
		for ei := range edges {
			e := &edges[ei]
			if e.isIndex && e.index == idx {
				p.matchDoc(d, elemByteStart, elemSi, e.child, cb)
				break
			}
		}
		if idx >= maxWanted {
			return
		}
		tj := d.skipValueIndex(elemSi)
		if tj >= len(s) {
			return
		}
		switch entryKind(s[tj]) {
		case kComma:
			idx++
			elemSi = tj + 1
			elemByteStart = skipWhitespace(d.data, entryOffset(s[tj])+1)
		case kCbrack:
			return
		default:
			return
		}
	}
}

// EachKeyDoc compiles paths and runs them over an indexed document in one call.
func EachKeyDoc(d *Document, cb func(idx int, value []byte, dataType ValueType, err error), paths ...[]string) {
	if len(paths) == 0 {
		return
	}
	CompileStrings(paths...).EachDoc(d, cb)
}
