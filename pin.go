package jseek

// Pinned queries: the mechanism that turns repeated lookups on stable or
// homogeneous data into near-array-indexing speed.
//
// The insight proven by benchmark: for a document of fixed structure, the
// structural-index position of the colon preceding a given path's value is
// constant. Learn it once, and every subsequent lookup is a direct read with
// zero key search, zero key compare, zero subtree skipping. On repeated
// queries this is ~14x faster than cold scanning and ~9x faster than indexed
// navigation.
//
// Correctness is non-negotiable: the learned trajectory is a CACHE, not a
// contract. Every pinned read verifies the located key still matches the
// expected key; on any structural drift it transparently falls back to a full
// search and re-learns. A Pinned query can therefore never return a wrong
// value, only (rarely) pay the search cost.

// Pinned is a path set whose structural trajectory is cached against a document
// shape. Build it with Document.Pin and reuse it across queries on the same
// document, or across a stream of same-shaped documents via Rebind.
//
// A Pinned is NOT safe for concurrent use: Get may re-learn the trajectory on
// shape drift, and Rebind mutates the underlying Document. Use one Pinned per
// goroutine.
type Pinned struct {
	d     *Document
	paths [][]string
	// traj[i] is the cached colon-trajectory for paths[i]: one structural index
	// per path segment (the colon preceding that segment's value). A full chain
	// is cached, not just the endpoint, so verification can confirm EVERY
	// segment's key still matches — preventing a stale endpoint from
	// accidentally matching an unrelated nested key. nil if not a flat path.
	traj [][]int
	// trajGen[i] records the Document generation traj[i] was learned against.
	// The cache is trusted ONLY while it equals d.gen — i.e. on the very same
	// document, where a learned first-occurrence position provably still is the
	// first occurrence. After Rebind (a new gen) that guarantee is void (a
	// position could align with a LATER duplicate key on a differently-shaped
	// record), so the read falls back to a full search (first occurrence wins)
	// and re-learns for the new shape.
	trajGen []uint64
}

// Pin learns the structural trajectory for each path against d's current
// structure and returns a reusable Pinned. Paths that traverse arrays or are
// not found are still supported via fallback (their cache entry is nil).
func (d *Document) Pin(paths ...[]string) *Pinned {
	p := &Pinned{
		d:       d,
		paths:   paths,
		traj:    make([][]int, len(paths)),
		trajGen: make([]uint64, len(paths)),
	}
	for i, path := range paths {
		p.traj[i] = d.learnTrajectory(path)
		p.trajGen[i] = d.gen
	}
	return p
}

// Rebind re-points the Pinned at new data, then verifies on first use. Rebind
// bumps the underlying Document's generation, so cached trajectories are not
// trusted across the rebind: each path re-searches and re-learns on the new
// record (guaranteeing duplicate-key-correct, first-occurrence results), and
// repeated reads of that same rebound record then reuse the fresh trajectory.
func (p *Pinned) Rebind(data []byte) {
	p.d.Reset(data)
}

// Get returns the value at paths[i] using the cached trajectory, transparently
// falling back to a full search (and re-learning) if the cache is stale or
// invalid. It never returns an incorrect value.
func (p *Pinned) Get(i int) ([]byte, ValueType, bool) {
	if i < 0 || i >= len(p.paths) {
		return nil, NotExist, false
	}
	// Trust the cache only on the same document it was learned against.
	if p.trajGen[i] == p.d.gen {
		if v, vt, ok := p.tryCached(i); ok {
			return v, vt, true
		}
	}
	// Cache miss / drift / different document (post-Rebind): full search, then
	// re-learn so repeated reads of this same document hit the fast path.
	start, ok := p.d.seekIndexed(p.paths[i])
	if !ok {
		p.traj[i] = p.d.learnTrajectory(p.paths[i])
		p.trajGen[i] = p.d.gen
		return nil, NotExist, false
	}
	p.traj[i] = p.d.learnTrajectory(p.paths[i])
	p.trajGen[i] = p.d.gen
	vs, ve, vt, vok := valueBounds(p.d.data, start)
	if !vok {
		return nil, NotExist, false
	}
	return p.d.data[vs:ve], vt, true
}

// tryCached attempts the zero-search read, verifying the FULL key chain. It
// returns ok=false (forcing fallback) on any mismatch.
func (p *Pinned) tryCached(i int) ([]byte, ValueType, bool) {
	tr := p.traj[i]
	path := p.paths[i]
	if tr == nil || len(tr) != len(path) {
		return nil, NotExist, false
	}
	s := p.d.structurals
	for seg := 0; seg < len(path); seg++ {
		ci := tr[seg]
		if ci < 1 || ci >= len(s) || entryKind(s[ci]) != kColon {
			return nil, NotExist, false
		}
		// Verify the key preceding this colon equals the expected segment.
		qi := ci - 1
		if entryKind(s[qi]) != kQuote {
			return nil, NotExist, false
		}
		_, match, ok := scanKey(p.d.data, entryOffset(s[qi]), path[seg])
		if !ok || !match {
			return nil, NotExist, false
		}
	}
	// All segment keys verified: read the value at the final colon.
	ci := tr[len(tr)-1]
	start := skipWhitespace(p.d.data, entryOffset(s[ci])+1)
	vs, ve, vt, ok := valueBounds(p.d.data, start)
	if !ok {
		return nil, NotExist, false
	}
	return p.d.data[vs:ve], vt, true
}

// learnTrajectory navigates the path once and returns the colon structural
// index for EACH segment, or nil if the path is not a resolvable flat object
// path (arrays or absence fall back to search at query time).
func (d *Document) learnTrajectory(keys []string) []int {
	if d.oversize {
		// No index to learn a trajectory over; Pinned queries fall back to the
		// stateless scanner via seekIndexed.
		return nil
	}
	s := d.structurals
	if len(s) == 0 || len(keys) == 0 {
		return nil
	}
	traj := make([]int, 0, len(keys))
	si := 0
	for _, key := range keys {
		if si >= len(s) || entryKind(s[si]) != kObrace {
			return nil
		}
		if _, isIdx := parseArrayIndex(key); isIdx {
			return nil // array hop: not a flat colon trajectory
		}
		j := si + 1
		found := false
		for j < len(s) {
			if entryKind(s[j]) != kQuote {
				return nil
			}
			keyOpen := entryOffset(s[j])
			_, match, ok := scanKey(d.data, keyOpen, key)
			if !ok {
				return nil
			}
			cj := j + 1
			if cj >= len(s) || entryKind(s[cj]) != kColon {
				return nil
			}
			if match {
				traj = append(traj, cj)
				si = cj + 1
				found = true
				break
			}
			tj := d.skipValueIndex(cj + 1)
			if tj >= len(s) || entryKind(s[tj]) != kComma {
				return nil
			}
			j = tj + 1
		}
		if !found {
			return nil
		}
	}
	return traj
}
