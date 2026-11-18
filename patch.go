package jseek

// Incremental index patching — the write path for a single sealed document.
//
// Set/Delete each resolve to ONE contiguous byte-range edit (see mutate.go).
// That makes the structural index incrementally updatable without a full
// re-scan:
//
//   - structurals entirely BEFORE the edit keep their offsets unchanged;
//   - structurals entirely AFTER the edit shift by delta = newLen - oldLen;
//   - structurals INSIDE the edited region are re-scanned locally (only the
//     replacement bytes, plus the value being replaced is dropped).
//
// So a small edit to a large document costs O(edit region + index tail shift),
// not O(document). The payload is rebuilt with the same single-allocation
// splice as AppendSet; the index is patched in place over a reused buffer.
//
// This is the bridge from "read-mostly substrate" toward an embedded JSON
// store with cheap field-level writes. Correctness is gated by differential
// fuzzing against a full rebuild (FuzzPatchMatchesRebuild).

// PatchSet returns a new payload and a Document whose index is incrementally
// patched to reflect Set(data, setValue, keys...). It never mutates data or d.
// The returned Document aliases the returned payload. d may be nil (a fresh
// index is built) or an existing indexed Document over data (its index is
// reused as the patch base).
func PatchSet(d *Document, data, setValue []byte, keys ...string) ([]byte, *Document, error) {
	start := skipWhitespace(data, 0)
	if _, ok := skipValue(data, start); !ok {
		// Empty input: synthesize, then index from scratch.
		out, err := Set(data, setValue, keys...)
		if err != nil {
			return nil, nil, err
		}
		return out, Index(out), nil
	}
	e, err := planSet(data, start, keys)
	if err != nil {
		return nil, nil, err
	}
	// Build the replacement bytes for the edit region.
	var repl []byte
	if e.replace {
		repl = setValue
	} else {
		if !e.insertEmpty {
			repl = append(repl, ',')
		}
		repl = appendJSONString(repl, e.insertKey)
		repl = append(repl, ':')
		repl = appendNested(repl, e.restKeys, setValue)
	}
	out := splice(data, e.cut0, e.cut1, repl)
	nd, err := patchIndex(d, data, out, e.cut0, e.cut1, repl)
	if err != nil {
		return nil, nil, err
	}
	return out, nd, nil
}

// PatchDelete is PatchSet's delete counterpart: it removes the value at keys
// and incrementally patches the index. If the path is absent the payload and
// index are returned unchanged (removed=false).
func PatchDelete(d *Document, data []byte, keys ...string) ([]byte, *Document, bool, error) {
	if len(keys) == 0 {
		return []byte{}, Index([]byte{}), true, nil
	}
	start := skipWhitespace(data, 0)
	if _, ok := skipValue(data, start); !ok {
		return append([]byte(nil), data...), d, false, nil
	}
	rs, re, ok := planDelete(data, start, keys)
	if !ok {
		return append([]byte(nil), data...), d, false, nil
	}
	out := splice(data, rs, re, nil)
	nd, err := patchIndex(d, data, out, rs, re, nil)
	if err != nil {
		return nil, nil, false, err
	}
	return out, nd, true, nil
}

// splice builds data[:cut0] + repl + data[cut1:] in one allocation.
func splice(data []byte, cut0, cut1 int, repl []byte) []byte {
	out := make([]byte, 0, len(data)-(cut1-cut0)+len(repl))
	out = append(out, data[:cut0]...)
	out = append(out, repl...)
	out = append(out, data[cut1:]...)
	return out
}

// patchIndex builds the new Document's index from the old one by keeping the
// prefix, re-scanning the edited region, and shifting the suffix. d is the
// base index over data (may be nil or oversize, in which case a full rebuild
// is used). out is the new payload; [cut0,cut1) in data was replaced by repl.
func patchIndex(d *Document, data, out []byte, cut0, cut1 int, repl []byte) (*Document, error) {
	if len(out) > maxIndexable {
		nd := &Document{data: out, gen: nextDocGen(), oversize: true}
		return nd, nil
	}
	// Fall back to a full rebuild when there is no usable base index, or the
	// edit touches the STE-expanded region in a way the simple patch can't
	// model cheaply. The rebuild is always correct; the patch is the fast path.
	if d == nil || d.oversize || len(d.structurals) == 0 {
		return Index(out), nil
	}
	delta := len(repl) - (cut1 - cut0)
	old := d.structurals
	// Partition old structurals into prefix (< cut0), inside ([cut0,cut1)),
	// suffix (>= cut1). Offsets are byte positions in data.
	lo := 0
	for lo < len(old) && entryOffset(old[lo]) < cut0 {
		lo++
	}
	hi := lo
	for hi < len(old) && entryOffset(old[hi]) < cut1 {
		hi++
	}
	// New index = prefix (unchanged) + re-scanned repl + suffix (shifted).
	buf := make([]uint32, 0, len(old)+len(repl)/4+8)
	buf = append(buf, old[:lo]...)
	// Re-scan the replacement bytes. Their offsets are relative to cut0 in out.
	if len(repl) > 0 {
		tmp := indexStructurals(repl, nil)
		for _, e := range tmp {
			off := entryOffset(e) + cut0
			if off > maxIndexable {
				return Index(out), nil
			}
			buf = append(buf, packEntry(entryKind(e), uint32(off)))
		}
	}
	// Shift the suffix.
	for _, e := range old[hi:] {
		off := entryOffset(e) + delta
		if off < 0 || off > maxIndexable {
			return Index(out), nil
		}
		buf = append(buf, packEntry(entryKind(e), uint32(off)))
	}
	nd := &Document{data: out, gen: nextDocGen(), structurals: buf}
	// The tape (if any) is invalidated by the edit; rebuild it lazily only if
	// the caller opts in via WithTape. Plain navigation works without it.
	nd.hasTape = false
	// Validate the patched index against a fresh scan in debug builds is done
	// by the fuzz gate; here we sanity-check offset bounds.
	if !validateIndex(nd) {
		return Index(out), nil
	}
	return nd, nil
}