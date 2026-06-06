package jseek

// Multi-path extraction: read many paths from a document in a SINGLE traversal,
// visiting shared prefixes once and skipping everything not requested.
//
// The matcher is compiled into a flat, slice-based automaton (no maps, no
// per-node pointers) so that querying is allocation-free. Compile a Paths value
// once and reuse it across many documents for zero-allocation hot loops; the
// EachKey / EachKeyStrings helpers are conveniences that compile on each call.

// Paths is a compiled, reusable set of query paths. Build it once with Compile
// (or CompileStrings) and call Each repeatedly; Each performs no allocation.
type Paths struct {
	nodes []pnode
}

// pnode is one node of the compiled automaton. edges and terminal index into
// the parent Paths' shared backing via small local slices; both are typically
// tiny (proportional to the number of requested paths), so linear scans beat
// map lookups and never allocate at query time.
type pnode struct {
	edges    []pedge
	terminal []int // indices (into the original paths slice) that end here
}

type pedge struct {
	isIndex bool
	index   int    // valid when isIndex
	key     string // valid when !isIndex (object key)
	child   int    // index into Paths.nodes
}

// CompileStrings compiles string paths (object keys and bracketed indices like
// "[0]") into a reusable Paths automaton.
func CompileStrings(paths ...[]string) *Paths {
	p := &Paths{nodes: make([]pnode, 1, len(paths)+1)} // node 0 is the root
	for idx, path := range paths {
		cur := 0
		for _, seg := range path {
			cur = p.descend(cur, seg)
		}
		p.nodes[cur].terminal = append(p.nodes[cur].terminal, idx)
	}
	return p
}

// Compile is like CompileStrings but accepts byte-slice segments, avoiding a
// string conversion when callers already hold bytes.
func Compile(paths ...[][]byte) *Paths {
	p := &Paths{nodes: make([]pnode, 1, len(paths)+1)}
	for idx, path := range paths {
		cur := 0
		for _, seg := range path {
			cur = p.descend(cur, string(seg))
		}
		p.nodes[cur].terminal = append(p.nodes[cur].terminal, idx)
	}
	return p
}

// descend returns the child node of `from` for segment seg, creating it if
// necessary.
func (p *Paths) descend(from int, seg string) int {
	idx, isIdx := parseArrayIndex(seg)
	// Reuse an existing edge if present (so shared prefixes share nodes).
	for _, e := range p.nodes[from].edges {
		if isIdx {
			if e.isIndex && e.index == idx {
				return e.child
			}
		} else if !e.isIndex && e.key == seg {
			return e.child
		}
	}
	child := len(p.nodes)
	p.nodes = append(p.nodes, pnode{})
	p.nodes[from].edges = append(p.nodes[from].edges, pedge{
		isIndex: isIdx,
		index:   idx,
		key:     seg,
		child:   child,
	})
	return child
}

// Each extracts all compiled paths from data in a single pass, invoking cb once
// per path that is found, with the path's original index. It performs no
// allocation. Values follow the same aliasing and quote-stripping rules as Get.
func (p *Paths) Each(data []byte, cb func(idx int, value []byte, dataType ValueType, err error)) {
	if len(p.nodes) == 0 {
		return
	}
	start := skipWhitespace(data, 0)
	p.match(data, start, 0, cb)
}

func (p *Paths) match(data []byte, i, ni int, cb func(int, []byte, ValueType, error)) {
	node := &p.nodes[ni]
	if len(node.terminal) > 0 {
		vs, ve, vt, ok := valueBounds(data, i)
		if !ok {
			for _, idx := range node.terminal {
				cb(idx, nil, NotExist, ErrMalformedJSON)
			}
		} else {
			val := data[vs:ve]
			for _, idx := range node.terminal {
				cb(idx, val, vt, nil)
			}
		}
	}
	if len(node.edges) == 0 || i >= len(data) {
		return
	}
	switch data[i] {
	case '{':
		p.matchObject(data, i, ni, cb)
	case '[':
		p.matchArray(data, i, ni, cb)
	}
}

func (p *Paths) matchObject(data []byte, oi, ni int, cb func(int, []byte, ValueType, error)) {
	i := skipWhitespace(data, oi+1)
	if i < len(data) && data[i] == '}' {
		return
	}
	// used tracks which edges have already matched within THIS object so that,
	// like Get, the first occurrence of a duplicate key wins. A stack-resident
	// bitmask keeps this allocation-free; for the pathological case of >64
	// distinct requested keys at one depth we simply stop deduplicating, which
	// only affects malformed duplicate-key input.
	var used uint64
	edges := p.nodes[ni].edges
	for i < len(data) {
		if data[i] != '"' {
			return
		}
		ks := i + 1
		ke, ok := skipString(data, i)
		if !ok {
			return
		}
		i = skipWhitespace(data, ke)
		if i >= len(data) || data[i] != ':' {
			return
		}
		i = skipWhitespace(data, i+1)

		// Linear scan of this node's edges for a matching object key. Edge
		// counts are tiny (number of distinct requested keys at this depth), so
		// this is faster than a map and allocates nothing.
		for ei := range edges {
			e := &edges[ei]
			if e.isIndex {
				continue
			}
			if ei < 64 && used&(1<<uint(ei)) != 0 {
				continue // already matched earlier in this object
			}
			if keyMatches(data, ks, ke-1, e.key) {
				if ei < 64 {
					used |= 1 << uint(ei)
				}
				p.match(data, i, e.child, cb)
				break
			}
		}
		var sok bool
		if i, sok = skipValue(data, i); !sok {
			return
		}
		i = skipWhitespace(data, i)
		if i >= len(data) {
			return
		}
		switch data[i] {
		case ',':
			i = skipWhitespace(data, i+1)
		case '}':
			return
		default:
			return
		}
	}
}

func (p *Paths) matchArray(data []byte, ai, ni int, cb func(int, []byte, ValueType, error)) {
	// Determine the highest index we actually care about so we can stop walking
	// the array early once all requested elements have been seen.
	maxWanted := -1
	hasIndexEdge := false
	for _, e := range p.nodes[ni].edges {
		if e.isIndex {
			hasIndexEdge = true
			if e.index > maxWanted {
				maxWanted = e.index
			}
		}
	}
	if !hasIndexEdge {
		return
	}

	i := skipWhitespace(data, ai+1)
	if i < len(data) && data[i] == ']' {
		return
	}
	idx := 0
	for i < len(data) {
		for _, e := range p.nodes[ni].edges {
			if e.isIndex && e.index == idx {
				p.match(data, i, e.child, cb)
				break
			}
		}
		if idx >= maxWanted {
			return // every requested element has been matched
		}
		var ok bool
		if i, ok = skipValue(data, i); !ok {
			return
		}
		i = skipWhitespace(data, i)
		if i >= len(data) {
			return
		}
		switch data[i] {
		case ',':
			i = skipWhitespace(data, i+1)
			idx++
		case ']':
			return
		default:
			return
		}
	}
}

// EachKey extracts multiple byte-slice paths in a single pass. It compiles the
// paths on each call; for repeated queries, prefer Compile + Paths.Each to
// avoid the compile cost.
func EachKey(data []byte, cb func(idx int, value []byte, dataType ValueType, err error), paths ...[][]byte) {
	if len(paths) == 0 {
		return
	}
	Compile(paths...).Each(data, cb)
}

// EachKeyStrings is EachKey for []string paths, matching the ergonomics of Get.
func EachKeyStrings(data []byte, cb func(idx int, value []byte, dataType ValueType, err error), paths ...[]string) {
	if len(paths) == 0 {
		return
	}
	CompileStrings(paths...).Each(data, cb)
}
