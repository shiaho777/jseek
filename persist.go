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
// File layout (little-endian):
//
//	magic      [8]byte  "JSEEKIX1"
//	version    uint32   format version (1)
//	flags      uint32   bit0 = tape present
//	dataLen    uint64   length of the raw JSON payload
//	nStruct    uint64   number of structural entries
//	dataHash   uint64   FNV-1a of the payload (integrity / staleness check)
//	--- header = 40 bytes ---
//	structurals [nStruct]uint32
//	tape        [nStruct]uint32   (only if flags bit0)
//
// The raw JSON payload is NOT stored in the index file by default: the caller
// keeps it (e.g. a .json next to a .jix), and Open pairs them. This keeps the
// index small and lets the payload be mmap'd independently. Use Seal to embed
// the payload for a single self-contained file.

const (
	persistMagic   = "JSEEKIX1"
	persistVersion = 1
	persistHeader  = 40
	flagTape       = 1 << 0
)

// ErrBadIndex is returned when a persisted index is corrupt, truncated, or
// does not match the payload it is paired with.
var ErrBadIndex = errors.New("jseek: invalid or mismatched persistent index")

// fnv1a64 computes a 64-bit FNV-1a hash, used to bind an index to its payload.
func fnv1a64(b []byte) uint64 {
	const (
		offset64 = 14611111111111656037
		prime64  = 1091111128211
	)
	h := uint64(offset64)
	for _, c := range b {
		h ^= uint64(c)
		h *= prime64
	}
	return h
}

// Persist writes d's structural index (and tape, if present) to w in the
// portable format. The raw payload is not written; pair it via Open. d must
// be a successfully indexed (non-oversize) Document.
func (d *Document) Persist(w io.Writer) error {
	if d.oversize {
		return ErrBadIndex
	}
	var hdr [persistHeader]byte
	copy(hdr[0:8], persistMagic)
	binary.LittleEndian.PutUint32(hdr[8:12], persistVersion)
	flags := uint32(0)
	if d.hasTape {
		flags |= flagTape
	}
	binary.LittleEndian.PutUint32(hdr[12:16], flags)
	binary.LittleEndian.PutUint64(hdr[16:24], uint64(len(d.data)))
	binary.LittleEndian.PutUint64(hdr[24:32], uint64(len(d.structurals)))
	binary.LittleEndian.PutUint64(hdr[32:40], fnv1a64(d.data))
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

// Open reconstructs a Document from a persisted index (r) paired with its raw
// payload (data). It performs NO Stage-1 scan: the structural index is read
// directly, so opening is O(index size) and independent of payload scanning.
// The payload hash is verified; a mismatch yields ErrBadIndex (the index is
// stale relative to the data). The returned Document aliases data.
func Open(r io.Reader, data []byte) (*Document, error) {
	var hdr [persistHeader]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, ErrBadIndex
	}
	if string(hdr[0:8]) != persistMagic {
		return nil, ErrBadIndex
	}
	if binary.LittleEndian.Uint32(hdr[8:12]) != persistVersion {
		return nil, ErrBadIndex
	}
	flags := binary.LittleEndian.Uint32(hdr[12:16])
	dataLen := binary.LittleEndian.Uint64(hdr[16:24])
	nStruct := binary.LittleEndian.Uint64(hdr[24:32])
	wantHash := binary.LittleEndian.Uint64(hdr[32:40])
	if dataLen != uint64(len(data)) {
		return nil, ErrBadIndex
	}
	if fnv1a64(data) != wantHash {
		return nil, ErrBadIndex
	}
	if nStruct > uint64(len(data))+1 {
		return nil, ErrBadIndex // sanity: cannot have more structurals than bytes
	}
	d := &Document{data: data, gen: nextDocGen()}
	var err error
	d.structurals, err = readU32s(r, int(nStruct))
	if err != nil {
		return nil, ErrBadIndex
	}
	if flags&flagTape != 0 {
		d.tape, err = readU32s(r, int(nStruct))
		if err != nil {
			return nil, ErrBadIndex
		}
		d.hasTape = true
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
// This is the "data substrate" fast path — opening allocates only the small
// Document struct, never the index arrays.
//
// Integrity: by default View verifies the payload hash (O(payload)). For the
// canonical mmap pattern — map the file once, verify once, then open many
// times — use ViewTrusted to skip the per-open hash after you have validated
// the mapping a single time with VerifyIndex. This makes each open O(1).
//
// idx must be the exact serialized index (header + arrays), little-endian,
// 4-byte aligned at the array region (persistHeader=40 is a multiple of 4).
// data is the raw payload the index was built from. The returned Document
// aliases both idx and data: neither may be mutated or freed while the
// Document is in use (keep the mmap alive for the Document's lifetime).
func View(idx, data []byte) (*Document, error) {
	return view(idx, data, true)
}

// ViewTrusted is View without the per-open payload hash check. Call
// VerifyIndex once on the mapping first; afterwards each open is O(1) and
// allocation-free apart from the Document struct. Use only when the idx/data
// pairing is already validated and the bytes are immutable (e.g. a read-only
// mmap of a sealed file).
func ViewTrusted(idx, data []byte) (*Document, error) {
	return view(idx, data, false)
}

// VerifyIndex checks that idx is a well-formed persisted index bound to data
// (magic, version, lengths, payload hash). Call it once after mapping a file;
// then use ViewTrusted for O(1) opens. Returns ErrBadIndex on any mismatch.
func VerifyIndex(idx, data []byte) error {
	_, err := view(idx, data, true)
	return err
}

func view(idx, data []byte, checkHash bool) (*Document, error) {
	if len(idx) < persistHeader {
		return nil, ErrBadIndex
	}
	if string(idx[0:8]) != persistMagic {
		return nil, ErrBadIndex
	}
	if binary.LittleEndian.Uint32(idx[8:12]) != persistVersion {
		return nil, ErrBadIndex
	}
	flags := binary.LittleEndian.Uint32(idx[12:16])
	dataLen := binary.LittleEndian.Uint64(idx[16:24])
	nStruct := binary.LittleEndian.Uint64(idx[24:32])
	wantHash := binary.LittleEndian.Uint64(idx[32:40])
	if dataLen != uint64(len(data)) {
		return nil, ErrBadIndex
	}
	need := persistHeader + int(nStruct)*4
	if flags&flagTape != 0 {
		need += int(nStruct) * 4
	}
	if need != len(idx) || nStruct > uint64(len(data))+1 {
		return nil, ErrBadIndex
	}
	if checkHash && fnv1a64(data) != wantHash {
		return nil, ErrBadIndex
	}
	d := &Document{data: data, gen: nextDocGen()}
	d.structurals = u32Slice(idx[persistHeader : persistHeader+int(nStruct)*4])
	if flags&flagTape != 0 {
		off := persistHeader + int(nStruct)*4
		d.tape = u32Slice(idx[off : off+int(nStruct)*4])
		d.hasTape = true
	}
	return d, nil
}