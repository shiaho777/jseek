package jseek

import (
	"sync"
	"sync/atomic"
)

// This file implements the "index once, query many" engine — the heart of
// jseek's SOTA design, mirroring the Stage-1/Stage-2 split popularized by
// simdjson.
//
// Stage 1 (Index): a single pass over the bytes records the position of every
// *structural* character — { } [ ] : , and the opening " of each string —
// while correctly accounting for string contents and escapes. The result is a
// compact array of indices.
//
// Stage 2 (Navigate): path queries walk this index array, which is far smaller
// than the raw document and contains only decision points. Crucially, skipping
// a nested subtree becomes a brace-depth scan over indices rather than a
// re-scan of every byte — so repeated queries on one document share the single
// Stage-1 cost.
//
// The Stage-1 inner loop uses SWAR (8 bytes/word). Hand-written AVX2/AVX-512
// and NEON kernels can replace indexStructurals behind the same signature.

// Document is a parsed structural index over a JSON document. Build it once
// with Index and issue many queries against it; each query reuses the shared
// structural index instead of re-scanning the raw bytes.
//
// A Document holds a reference to the original data and does not copy it, so
// the data must remain unmodified and live for the Document's lifetime.
//
// The read methods (Get, GetString, GetInt, ..., Exists, and EachDoc) are
// read-only and safe for concurrent use on a single Document. The mutating
// methods — Reset, WithTape, Free, and Pin (whose returned cache re-learns on
// drift) — are not; do not call them concurrently with reads or each other.
type Document struct {
	data []byte
	// structurals holds packed entries, in document order. Each entry packs a
	// 3-bit structural kind in the high bits with the 29-bit byte offset in the
	// low bits (see packEntry). Storing the kind inline lets navigation read
	// only this sequential array instead of chasing a random load into data for
	// every structural — a large cache win on big documents, at no extra
	// memory (still one uint32 per structural).
	// tape, when hasTape is set, holds matching open/close indices so a whole
	// container can be skipped in O(1) (see index_tape.go). It is opt-in.
	structurals []uint32
	tape        []uint32
	tapeStack   []uint32
	hasTape     bool
	// gen is a monotonically increasing identity stamped each time the Document
	// is (re)pointed at bytes (Index/IndexPooled/Reset). A Pinned trajectory
	// cache records the gen it was learned against and only trusts itself while
	// gen is unchanged — i.e. on the SAME document, where a learned
	// first-occurrence position is provably still the first occurrence. After a
	// Reset/Rebind the gen differs and the cache falls back to a full search,
	// which keeps duplicate-key results correct (first occurrence wins).
	gen uint64
	// oversize is set when the document exceeds the indexable ceiling
	// (maxIndexable). The structural index would overflow its 29-bit offsets,
	// so we build no index and every query transparently routes to the
	// unlimited stateless scanner. Results stay correct at any size; only the
	// per-query "index once" amortization is forgone for such giants (for which
	// streaming is the recommended approach anyway).
	oversize bool
}

// docGen sources unique Document generations. Documents are not safe for
// concurrent mutation, but the counter is atomic so distinct Documents built
// concurrently never collide on a generation value.
var docGen uint64

func nextDocGen() uint64 { return atomic.AddUint64(&docGen, 1) }

// Structural kind codes packed into each index entry.
const (
	kNone   uint32 = 0
	kObrace uint32 = 1 // {
	kCbrace uint32 = 2 // }
	kObrack uint32 = 3 // [
	kCbrack uint32 = 4 // ]
	kColon  uint32 = 5 // :
	kComma  uint32 = 6 // ,
	kQuote  uint32 = 7 // "
)

const (
	offsetBits = 29
	offsetMask = (1 << offsetBits) - 1
)

// maxIndexable is the largest document the indexed engine supports (512 MiB).
// Beyond this, packing would overflow the 29-bit offset, so Index builds no
// index and queries transparently use the unlimited stateless scanner; callers
// indexing such giants should prefer streaming. It is a var (not a const) only
// so tests can lower the ceiling to exercise the fallback without allocating
// half a gigabyte; production never mutates it. This ceiling is far above any
// realistic lazy-extraction workload.
var maxIndexable = offsetMask

func packEntry(kind, off uint32) uint32 { return kind<<offsetBits | off }
func entryKind(e uint32) uint32         { return e >> offsetBits }
func entryOffset(e uint32) int          { return int(e & offsetMask) }

// structuralKind maps a byte to its packed kind code (kNone if not structural).
var structuralKind = func() [256]uint32 {
	var t [256]uint32
	t['{'] = kObrace
	t['}'] = kCbrace
	t['['] = kObrack
	t[']'] = kCbrack
	t[':'] = kColon
	t[','] = kComma
	t['"'] = kQuote
	return t
}()

var indexPool = sync.Pool{
	New: func() any { return new(Document) },
}

// Index performs a single Stage-1 pass over data and returns a reusable
// Document. The returned Document aliases data.
//
// If data is not structurally well-formed enough to index (unterminated
// string), Index still returns a Document covering what it could scan; query
// methods then surface ErrMalformedJSON where appropriate. Index never
// allocates the index on the data itself, only the (poolable) offset array.
func Index(data []byte) *Document {
	d := &Document{data: data, gen: nextDocGen()}
	if len(data) > maxIndexable {
		// Too large to index with 29-bit offsets: serve queries from the
		// unlimited stateless scanner instead (see Document.oversize).
		d.oversize = true
		return d
	}
	d.structurals = make([]uint32, 0, len(data)/8+8)
	d.structurals = indexStructurals(data, d.structurals)
	return d
}

// Free returns the Document (and its buffers) to an internal pool for reuse.
// After Free the Document must not be used again. Free is optional; it reduces
// GC pressure in high-throughput services that index many documents. Only
// Documents obtained from IndexPooled / IndexTapePooled should be Freed.
func (d *Document) Free() {
	if d == nil {
		return
	}
	d.data = nil
	d.structurals = d.structurals[:0]
	d.tape = d.tape[:0]
	d.tapeStack = d.tapeStack[:0]
	d.hasTape = false
	d.oversize = false
	indexPool.Put(d)
}

// Reset re-points an existing Document at new data, reusing the already-
// allocated index buffer. This is the zero-allocation way to process a stream
// of documents: keep one Document and Reset it for each. It returns d for
// chaining.
func (d *Document) Reset(data []byte) *Document {
	d.data = data
	d.hasTape = false
	d.gen = nextDocGen()
	if len(data) > maxIndexable {
		d.oversize = true
		if d.structurals != nil {
			d.structurals = d.structurals[:0]
		}
		return d
	}
	d.oversize = false
	if d.structurals == nil {
		d.structurals = make([]uint32, 0, len(data)/8+8)
	} else {
		d.structurals = d.structurals[:0]
	}
	d.structurals = indexStructurals(data, d.structurals)
	return d
}

// IndexPooled is like Index but draws the Document and its buffers from a pool,
// to be returned by Document.Free. Use it on hot paths that index a document
// per request.
func IndexPooled(data []byte) *Document {
	d := indexPool.Get().(*Document)
	d.data = data
	d.hasTape = false
	d.gen = nextDocGen()
	if len(data) > maxIndexable {
		d.oversize = true
		d.structurals = d.structurals[:0]
		return d
	}
	d.oversize = false
	if cap(d.structurals) < len(data)/8+8 {
		d.structurals = make([]uint32, 0, len(data)/8+8)
	} else {
		d.structurals = d.structurals[:0]
	}
	d.structurals = indexStructurals(data, d.structurals)
	return d
}

// IndexTapePooled is IndexPooled plus the skip tape, all drawn from the pool, so
// per-request tape-accelerated navigation is allocation-free after warm-up.
// Return it with Document.Free.
func IndexTapePooled(data []byte) *Document {
	d := IndexPooled(data)
	d.buildTape()
	return d
}

// indexStructurals scans data and appends a packed entry (kind + offset) for
// every structural character and every string's opening quote, returning the
// result.
//
// It walks string bodies with the SWAR quote/backslash scanner so the bulk of
// payload bytes (string contents) are traversed at register-parallel speed,
// and only structural decision points are recorded. If the document is larger
// than the indexable ceiling, scanning stops; the indexed API then reports
// not-found for paths beyond the indexed region (the stateless Get API has no
// size limit).
func indexStructurals(data []byte, out []uint32) []uint32 {
	n := len(data)
	if n > maxIndexable {
		n = maxIndexable
	}
	i := 0
	for i < n {
		c := data[i]
		if c == '"' {
			out = append(out, packEntry(kQuote, uint32(i)))
			end, ok := skipString(data, i)
			if !ok {
				return out
			}
			i = end
			continue
		}
		if k := structuralKind[c]; k != kNone {
			out = append(out, packEntry(k, uint32(i)))
			i++
			continue
		}
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			i = indexSkipWhitespace(data, i)
			continue
		}
		i++
	}
	return out
}
