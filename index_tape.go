package jseek

// Skip-pointer tape: an optional acceleration structure for navigation.
//
// The linear skipValueIndex walks every structural inside a subtree to find its
// end — O(subtree size). When navigating to a deep array element or past large
// objects, this dominates (it is the top CPU hotspot in profiles). The tape
// precomputes, for every open '{'/'[' entry, the index of its matching closer
// (and vice versa), so skipping an entire container becomes O(1).
//
// Cost: one extra uint32 per structural (the tape array), i.e. it doubles the
// transient index size. That memory is released with the Document, and for the
// "index once, query many" use case the navigation speedup outweighs it. The
// tape is therefore opt-in via IndexTape / WithTape, leaving plain Index lean.

// buildTape computes matching open/close indices for d.structurals. tape[k] for
// an open-container entry holds the position of its matching closer; for a
// closer it holds the matching opener. For all other entries it is 0 and
// unused. A small stack tracks open containers during the single pass.
func (d *Document) buildTape() {
	if d.oversize {
		// No structural index to build a tape over; navigation falls back to
		// the stateless scanner.
		d.hasTape = false
		return
	}
	s := d.structurals
	if cap(d.tape) >= len(s) {
		d.tape = d.tape[:len(s)]
		for i := range d.tape {
			d.tape[i] = 0
		}
	} else {
		d.tape = make([]uint32, len(s))
	}
	// Reuse a stack buffer across calls.
	stack := d.tapeStack[:0]
	for k := 0; k < len(s); k++ {
		switch entryKind(s[k]) {
		case kObrace, kObrack:
			stack = append(stack, uint32(k))
		case kCbrace, kCbrack:
			if len(stack) == 0 {
				continue // malformed; leave unset
			}
			open := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			d.tape[open] = uint32(k)
			d.tape[k] = open
		}
	}
	d.tapeStack = stack[:0]
	d.hasTape = true
}

// skipValueIndexTape is the O(1) counterpart to skipValueIndex, used when a tape
// is present. For a container it jumps straight past the matching closer.
func (d *Document) skipValueIndexTape(vj int) int {
	s := d.structurals
	if vj >= len(s) {
		return vj
	}
	switch entryKind(s[vj]) {
	case kObrace, kObrack:
		close := int(d.tape[vj])
		if close <= vj {
			return len(s) // unmatched (malformed)
		}
		return close + 1
	case kQuote:
		return vj + 1
	default:
		return vj
	}
}

// IndexTape is like Index but also builds the skip-pointer tape, accelerating
// navigation through large/deep structures at the cost of doubling the
// transient index size.
func IndexTape(data []byte) *Document {
	d := Index(data)
	d.buildTape()
	return d
}

// WithTape builds (or rebuilds) the skip tape on an existing Document and
// returns it, so callers can opt into tape acceleration after Index/Reset.
func (d *Document) WithTape() *Document {
	d.buildTape()
	return d
}
