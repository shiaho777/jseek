package jseek

import (
	"encoding/binary"
	"errors"
	"io"
)

// Persistent structural index — the "SQLite of JSON" foundation.
//
// jseek's Stage-1 index is a compact, pointer-free []uint32 (3-bit kind +
// 29-bit offset per structural). That makes it trivially serializable and
// mmap-friendly: a Document can be persisted once and re-opened by any
// process with ZERO re-scanning of the raw bytes. This turns jseek from a
// per-process extractor into a shareable, zero-parse JSON storage substrate.
//
// Index file layout (little-endian):
//
//	magic      [8]byte  "JSEEKIX1"
//	version    uint32   format version (1)
//	flags      uint32   bit0 = tape present, bit1 = fast payload hash
//	dataLen    uint64   length of the raw JSON payload
//	nStruct    uint64   number of structural entries
//	dataHash   uint64   payload integrity hash (algorithm per flags bit1)
//	--- header = 40 bytes ---
//	structurals [nStruct]uint32
//	tape        [nStruct]uint32   (only if flags bit0)
//
// The raw JSON payload is NOT stored in the index file: the caller keeps it
// (e.g. a .json next to a .jix) and Open/View pair them. For a single
// self-contained file use Seal / OpenSealed / ViewSealed (store.go), which
// wrap the payload and index in a "JSEEKST1" container.
//
// The format is experimental (pre-1.0): no compatibility guarantees yet.

const (
	persistMagic   = "JSEEKIX1"
	persistVersion = 1
	persistHeader  = 40
	flagTape       = 1 << 0
	flagFastHash   = 1 << 1
)

// ErrBadIndex is returned when a persisted index is corrupt, truncated, or
// does not match the payload it is paired with.
var ErrBadIndex = errors.New("jseek: invalid or mismatched persistent index")

// payloadHash binds an index to its payload. It is a word-at-a-time
// multiply-xor-rotate mix (~8 bytes/step, an order of magnitude faster than
// byte-wise FNV on large payloads). It is an integrity/staleness check, NOT
// a cryptographic digest.
func payloadHash(b []byte) uint64 {
	const (
		m1 = 0x9E3779B185EBCA87
		m2 = 0xC2B2AE3D27D4EB4F
	)
	h := uint64(0x243F6A8885A308D3) ^ uint64(len(b))
	i := 0
	for ; i+8 <= len(b); i += 8 {
		w := binary.LittleEndian.Uint64(b[i:])
		h ^= w * m1
		h = (h << 31) | (h >> 33)
		h *= m2
	}
	if i < len(b) {
		var w uint64
		for j := i; j < len(b); j++ {
			w |= uint64(b[j]) << (8 * (j - i))
		}
		h ^= w * m1
		h = (h << 31) | (h >> 33)
		h *= m2
	}
	h ^= h >> 33
	h *= m1
	h ^= h >> 29
	return h
}

// writeIndexHeader emits the 40-byte header for d into hdr.
func (d *Document) writeIndexHeader(hdr *[persistHeader]byte) {
	copy(hdr[0:8], persistMagic)
	binary.LittleEndian.PutUint32(hdr[8:12], persistVersion)
	flags := uint32(flagFastHash)
	if d.hasTape {
		flags |= flagTape
	}
	binary.LittleEndian.PutUint32(hdr[12:16], flags)
	binary.LittleEndian.PutUint64(hdr[16:24], uint64(len(d.data)))
	binary.LittleEndian.PutUint64(hdr[24:32], uint64(len(d.structurals)))
	binary.LittleEndian.PutUint64(hdr[32:40], payloadHash(d.data))
}

// Persist writes d's structural index (and tape, if present) to w in the
// portable format. The raw payload is not written; pair it via Open/View.
// d must be a successfully indexed (non-oversize) Document.
func (d *Document) Persist(w io.Writer) error {
	if d.oversize {
		return ErrBadIndex
	}
	var hdr [persistHeader]byte
	d.writeIndexHeader(&hdr)
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if err := writeU32s(w, d.structurals); err != nil {
		return err
	}
	if d.hasTape {
		if err := writeU32s(w, d.tape); err != nil {
			return err
		}
	}
	return nil
}

func writeU32s(w io.Writer, s []uint32) error {
	// Encode in chunks to avoid a huge temporary allocation.
	var buf [4096]byte
	for len(s) > 0 {
		n := len(s)
		if n > len(buf)/4 {
			n = len(buf) / 4
		}
		for i := 0; i < n; i++ {
			binary.LittleEndian.PutUint32(buf[i*4:], s[i])
		}
		if _, err := w.Write(buf[:n*4]); err != nil {
			return err
		}
		s = s[n:]
	}
	return nil
}

// indexHeader is the decoded, validated header of a persisted index.
type indexHeader struct {
	flags   uint32
	dataLen uint64
	nStruct uint64
	hash    uint64
}

func parseIndexHeader(hdr []byte) (indexHeader, bool) {
	if len(hdr) < persistHeader {
		return indexHeader{}, false
	}
	if string(hdr[0:8]) != persistMagic {
		return indexHeader{}, false
	}
	if binary.LittleEndian.Uint32(hdr[8:12]) != persistVersion {
		return indexHeader{}, false
	}
	h := indexHeader{
		flags:   binary.LittleEndian.Uint32(hdr[12:16]),
		dataLen: binary.LittleEndian.Uint64(hdr[16:24]),
		nStruct: binary.LittleEndian.Uint64(hdr[24:32]),
		hash:    binary.LittleEndian.Uint64(hdr[32:40]),
	}
	if h.flags & ^uint32(flagTape|flagFastHash) != 0 {
		return indexHeader{}, false // unknown flag bits
	}
	return h, true
}

func (h indexHeader) checkHash(data []byte) bool {
	if h.dataLen != uint64(len(data)) {
		return false
	}
	return payloadHash(data) == h.hash
}

// Open reconstructs a Document from a persisted index (r) paired with its raw
// payload (data). It performs NO Stage-1 scan: the structural index is read
// directly. The payload hash and every index offset are verified; any
// mismatch yields ErrBadIndex. The returned Document aliases data.
func Open(r io.Reader, data []byte) (*Document, error) {
	var hdr [persistHeader]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, ErrBadIndex
	}
	h, ok := parseIndexHeader(hdr[:])
	if !ok || !h.checkHash(data) {
		return nil, ErrBadIndex
	}
	if h.nStruct > uint64(len(data))+1 {
		return nil, ErrBadIndex
	}
	d := &Document{data: data, gen: nextDocGen()}
	var err error
	d.structurals, err = readU32s(r, int(h.nStruct))
	if err != nil {
		return nil, ErrBadIndex
	}
	if h.flags&flagTape != 0 {
		d.tape, err = readU32s(r, int(h.nStruct))
		if err != nil {
			return nil, ErrBadIndex
		}
		d.hasTape = true
	}
	if !validateIndex(d) {
		return nil, ErrBadIndex
	}
	return d, nil
}

func readU32s(r io.Reader, n int) ([]uint32, error) {
	if n < 0 {
		return nil, ErrBadIndex
	}
	s := make([]uint32, n)
	var buf [4096]byte
	for i := 0; i < n; {
		chunk := n - i
		if chunk > len(buf)/4 {
			chunk = len(buf) / 4
		}
		if _, err := io.ReadFull(r, buf[:chunk*4]); err != nil {
			return nil, err
		}
		for j := 0; j < chunk; j++ {
			s[i+j] = binary.LittleEndian.Uint32(buf[j*4:])
		}
		i += chunk
	}
	return s, nil
}

// IndexSize returns the serialized byte size of d's index (header + arrays),
// useful for sizing storage and comparing against the raw payload.
func (d *Document) IndexSize() int {
	n := persistHeader + len(d.structurals)*4
	if d.hasTape {
		n += len(d.tape) * 4
	}
	return n
}

// View opens a persisted index that is already resident in memory (e.g. an
// mmap'd file) as a Document with ZERO copying and ZERO decoding: the
// structural and tape arrays are reinterpreted directly from idx's bytes.
// Opening allocates only the small Document struct, never the index arrays.
//
// Integrity: View verifies the payload hash (word-parallel, ~GB/s) and every
// index offset. For the canonical mmap pattern — map once, verify once, open
// many — use VerifyIndex once and then ViewTrusted for O(1) opens.
//
// idx must be the exact serialized index (header + arrays), little-endian.
// data is the raw payload. The returned Document aliases both idx and data:
// neither may be mutated or freed while the Document is in use (keep the
// mmap alive for the Document's lifetime).
func View(idx, data []byte) (*Document, error) {
	return view(idx, data, true)
}

// ViewTrusted is View without the per-open payload hash check (offset bounds
// are still validated). Call VerifyIndex once on the mapping first; then
// each open is O(1). Use only when the idx/data pairing is already validated
// and the bytes are immutable (e.g. a read-only mmap of a sealed file).
func ViewTrusted(idx, data []byte) (*Document, error) {
	return view(idx, data, false)
}

// VerifyIndex checks that idx is a well-formed persisted index bound to data
// (magic, version, lengths, payload hash, offset bounds). Call it once after
// mapping a file; then use ViewTrusted for O(1) opens.
func VerifyIndex(idx, data []byte) error {
	_, err := view(idx, data, true)
	return err
}

func view(idx, data []byte, checkHash bool) (*Document, error) {
	h, ok := parseIndexHeader(idx)
	if !ok {
		return nil, ErrBadIndex
	}
	if h.dataLen != uint64(len(data)) {
		return nil, ErrBadIndex
	}
	need := persistHeader + int(h.nStruct)*4
	if h.flags&flagTape != 0 {
		need += int(h.nStruct) * 4
	}
	if need != len(idx) || h.nStruct > uint64(len(data))+1 {
		return nil, ErrBadIndex
	}
	if checkHash && !h.checkHash(data) {
		return nil, ErrBadIndex
	}
	d := &Document{data: data, gen: nextDocGen()}
	d.structurals = u32Slice(idx[persistHeader : persistHeader+int(h.nStruct)*4])
	if h.flags&flagTape != 0 {
		off := persistHeader + int(h.nStruct)*4
		d.tape = u32Slice(idx[off : off+int(h.nStruct)*4])
		d.hasTape = true
	}
	// Full offset validation runs with the hash check (View / VerifyIndex /
	// Open). The trusted path skips it: VerifyIndex already validated the
	// immutable mapping, keeping each trusted open O(1).
	if checkHash && !validateIndex(d) {
		return nil, ErrBadIndex
	}
	return d, nil
}

// validateIndex checks that every structural offset lies within the payload
// and every tape close offset is sane, so a corrupt-but-hash-matching index
// (or a trusted view of damaged bytes) can never drive navigation out of
// bounds. O(nStruct), no allocation.
func validateIndex(d *Document) bool {
	n := len(d.data)
	for _, e := range d.structurals {
		if int(entryOffset(e)) > n {
			return false
		}
	}
	if d.hasTape {
		if len(d.tape) != len(d.structurals) {
			return false
		}
		for _, c := range d.tape {
			if int(c) > n {
				return false
			}
		}
	}
	return true
}