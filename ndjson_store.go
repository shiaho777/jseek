package jseek

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"os"
)

// NDJSON random-access store — the substrate for the largest large-JSON shape:
// logs, events, metrics, one JSON object per line. A plain .ndjson file can
// only be scanned sequentially; this store adds a per-record offset table plus
// a sealed structural index per record, so record N is reachable in O(1) and
// queryable with zero re-parsing. Records are independent, which also makes
// appending genuinely incremental (O(one record), no rewrite of prior data).
//
// File layout (little-endian), magic "JSEEKNJ1":
//
//	header:
//	  magic     [8]byte
//	  version   uint32 (1)
//	  flags     uint32 (0)
//	  nRecords  uint64
//	  dataLen   uint64  total payload bytes (the concatenated raw lines)
//	--- 32 bytes ---
//	payload:  dataLen bytes (raw NDJSON, newline-separated, no trailing blank)
//	pad:      to 4-byte alignment
//	offsets:  (nRecords+1) uint64  — byte range [off[i], off[i+1]) of record i
//	                                 within payload (off[nRecords] == dataLen)
//	indices:  nRecords sealed indexes (each a Persist blob, tape included),
//	          concatenated; index i covers payload[off[i]:off[i+1]].
//
// The offset table is the random-access key; the per-record indexes make each
// record a zero-parse Document. Opening maps the file, reads the offset table
// once, and validates record bounds; Record(i) then views index i in O(1).

const (
	ndjsonMagic   = "JSEEKNJ1"
	ndjsonVersion = 1
	ndjsonHeader  = 32
	// ndjsonFlagFieldIndex marks an optional trailing field-index section that
	// lets WHERE queries skip non-matching records via binary search.
	ndjsonFlagFieldIndex = 1 << 0
)

// SealNDJSON builds a random-access store from NDJSON input (one JSON value per
// line; blank lines skipped). Each record is validated and indexed with the
// skip tape. Returns the number of records sealed.
func SealNDJSON(w io.Writer, r io.Reader) (int, error) {
	return sealNDJSON(w, r, nil)
}

// SealNDJSONIndexed is SealNDJSON plus a field index over the given dotted
// fields. The index lets WHERE queries on those fields locate matching records
// by binary search instead of scanning every record. Fields absent from a record
// are simply not indexed for it. The index is an optional trailing section;
// stores without it remain fully readable.
func SealNDJSONIndexed(w io.Writer, r io.Reader, fields []string) (int, error) {
	return sealNDJSON(w, r, fields)
}

func sealNDJSON(w io.Writer, r io.Reader, fields []string) (int, error) {
	// First pass: collect raw records and their byte ranges.
	var payload bytes.Buffer
	var offsets []uint64
	offsets = append(offsets, 0)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 256<<10), 64<<20)
	n := 0
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if !Valid(line) {
			return 0, ErrMalformedJSON
		}
		payload.Write(line)
		payload.WriteByte('\n')
		offsets = append(offsets, uint64(payload.Len()))
		n++
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	data := payload.Bytes()

	// Build per-record indexes over the shared payload.
	docs := make([]*Document, n)
	for i := 0; i < n; i++ {
		rec := data[offsets[i]:offsets[i+1]]
		rec = bytes.TrimRight(rec, "\n")
		docs[i] = IndexTape(rec)
		if docs[i].oversize {
			return 0, ErrBadIndex
		}
	}

	// Header.
	var flags uint32
	if len(fields) > 0 {
		flags |= ndjsonFlagFieldIndex
	}
	var hdr [ndjsonHeader]byte
	copy(hdr[0:8], ndjsonMagic)
	binary.LittleEndian.PutUint32(hdr[8:12], ndjsonVersion)
	binary.LittleEndian.PutUint32(hdr[12:16], flags)
	binary.LittleEndian.PutUint64(hdr[16:24], uint64(n))
	binary.LittleEndian.PutUint64(hdr[24:32], uint64(len(data)))
	if _, err := w.Write(hdr[:]); err != nil {
		return 0, err
	}
	if _, err := w.Write(data); err != nil {
		return 0, err
	}
	if pad := (4 - len(data)%4) % 4; pad > 0 {
		if _, err := w.Write(make([]byte, pad)); err != nil {
			return 0, err
		}
	}
	// Offset table.
	if err := writeU64s(w, offsets); err != nil {
		return 0, err
	}
	// Per-record indexes.
	for i := 0; i < n; i++ {
		if err := docs[i].Persist(w); err != nil {
			return 0, err
		}
	}
	// Optional field-index section.
	if len(fields) > 0 {
		if err := writeFieldIndex(w, docs, fields); err != nil {
			return 0, err
		}
	}
	return n, nil
}

func writeU64s(w io.Writer, s []uint64) error {
	var buf [4096]byte
	for len(s) > 0 {
		n := len(s)
		if n > len(buf)/8 {
			n = len(buf) / 8
		}
		for i := 0; i < n; i++ {
			binary.LittleEndian.PutUint64(buf[i*8:], s[i])
		}
		if _, err := w.Write(buf[:n*8]); err != nil {
			return err
		}
		s = s[n:]
	}
	return nil
}

// NDJSONStore is an mmap-backed random-access NDJSON store. Open once, then
// Record(i) / Len() / Each in O(1) per record with zero re-parsing. Safe for
// concurrent reads (the mapping is immutable).
type NDJSONStore struct {
	image   []byte
	f       *os.File
	data    []byte
	offsets []uint64
	// idxOff[i] is the byte offset of record i's sealed index within image.
	idxOff []uint64
	n      int
	// recIdxEnd is the byte offset just past the last per-record index (i.e.
	// where the optional field-index section begins, or len(image) if none).
	recIdxEnd uint64
	// fields holds the optional field index (nil when the store has none).
	fields []fieldIndex
}

// OpenNDJSONStore maps and validates a sealed NDJSON store.
func OpenNDJSONStore(path string) (*NDJSONStore, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	img, err := mmapReadOnly(f, int(st.Size()))
	if err != nil {
		f.Close()
		return nil, err
	}
	s, err := parseNDJSON(img)
	if err != nil {
		munmap(img)
		f.Close()
		return nil, err
	}
	s.image = img
	s.f = f
	return s, nil
}

func parseNDJSON(img []byte) (*NDJSONStore, error) {
	if len(img) < ndjsonHeader || string(img[0:8]) != ndjsonMagic {
		return nil, ErrBadIndex
	}
	if err := checkVersion(FormatNDJSON, binary.LittleEndian.Uint32(img[8:12])); err != nil {
		return nil, err
	}
	flags := binary.LittleEndian.Uint32(img[12:16])
	n := binary.LittleEndian.Uint64(img[16:24])
	dataLen := binary.LittleEndian.Uint64(img[24:32])
	if n > uint64(len(img)) || dataLen > uint64(len(img)) {
		return nil, ErrBadIndex
	}
	pad := (4 - dataLen%4) % 4
	offStart := ndjsonHeader + dataLen + pad
	offBytes := (n + 1) * 8
	if offStart+offBytes > uint64(len(img)) {
		return nil, ErrBadIndex
	}
	offsets := make([]uint64, n+1)
	for i := uint64(0); i <= n; i++ {
		offsets[i] = binary.LittleEndian.Uint64(img[offStart+i*8:])
	}
	if offsets[0] != 0 || offsets[n] != dataLen {
		return nil, ErrBadIndex
	}
	for i := uint64(0); i < n; i++ {
		if offsets[i] > offsets[i+1] {
			return nil, ErrBadIndex
		}
	}
	data := img[ndjsonHeader : ndjsonHeader+dataLen]
	// Walk the per-record indexes, recording their offsets and validating each
	// against its record slice (hash + bounds). O(total index size) once.
	idxOff := make([]uint64, n)
	pos := offStart + offBytes
	for i := uint64(0); i < n; i++ {
		idxOff[i] = pos
		rec := bytes.TrimRight(data[offsets[i]:offsets[i+1]], "\n")
		// Determine this index's length from its header.
		if pos+persistHeader > uint64(len(img)) {
			return nil, ErrBadIndex
		}
		h, ok := parseIndexHeader(img[pos:])
		if !ok {
			return nil, ErrBadIndex
		}
		idxLen := uint64(persistHeader) + h.nStruct*4
		if h.flags&flagTape != 0 {
			idxLen += h.nStruct * 4
		}
		if pos+idxLen > uint64(len(img)) {
			return nil, ErrBadIndex
		}
		if err := VerifyIndex(img[pos:pos+idxLen], rec); err != nil {
			return nil, err
		}
		pos += idxLen
	}
	recIdxEnd := pos
	var fields []fieldIndex
	if flags&ndjsonFlagFieldIndex != 0 {
		var err error
		fields, pos, err = parseFieldIndex(img, pos)
		if err != nil {
			return nil, err
		}
	}
	if pos != uint64(len(img)) {
		return nil, ErrBadIndex // trailing garbage
	}
	return &NDJSONStore{image: img, data: data, offsets: offsets, idxOff: idxOff, n: int(n), recIdxEnd: recIdxEnd, fields: fields}, nil
}

// Len returns the number of records.
func (s *NDJSONStore) Len() int { return s.n }

// Record returns a zero-parse Document over record i (0-based) in O(1). The
// Document aliases the mapping and is valid until Close. i out of range yields
// ErrKeyPathNotFound.
func (s *NDJSONStore) Record(i int) (*Document, error) {
	if i < 0 || i >= s.n {
		return nil, ErrKeyPathNotFound
	}
	rec := bytes.TrimRight(s.data[s.offsets[i]:s.offsets[i+1]], "\n")
	start := s.idxOff[i]
	var end uint64
	if i+1 < s.n {
		end = s.idxOff[i+1]
	} else {
		end = s.recIdxEnd
	}
	return ViewTrusted(s.image[start:end], rec)
}

// RecordInto is the zero-allocation Record: it re-points d at record i.
func (s *NDJSONStore) RecordInto(d *Document, i int) (*Document, error) {
	if i < 0 || i >= s.n {
		return nil, ErrKeyPathNotFound
	}
	rec := bytes.TrimRight(s.data[s.offsets[i]:s.offsets[i+1]], "\n")
	start := s.idxOff[i]
	var end uint64
	if i+1 < s.n {
		end = s.idxOff[i+1]
	} else {
		end = s.recIdxEnd
	}
	return ViewInto(d, s.image[start:end], rec)
}

// Raw returns record i's raw JSON bytes (aliasing the mapping).
func (s *NDJSONStore) Raw(i int) ([]byte, error) {
	if i < 0 || i >= s.n {
		return nil, ErrKeyPathNotFound
	}
	return bytes.TrimRight(s.data[s.offsets[i]:s.offsets[i+1]], "\n"), nil
}

// Each visits every record in order with a zero-parse Document. Return false
// from fn to stop early. The Document is reused across iterations (zero alloc).
func (s *NDJSONStore) Each(fn func(i int, d *Document) bool) error {
	var d Document
	for i := 0; i < s.n; i++ {
		if _, err := s.RecordInto(&d, i); err != nil {
			return err
		}
		if !fn(i, &d) {
			return nil
		}
	}
	return nil
}

// Close releases the mapping. Documents from Record must not be used after.
func (s *NDJSONStore) Close() error {
	if s.image != nil {
		munmap(s.image)
		s.image = nil
	}
	if s.f != nil {
		err := s.f.Close()
		s.f = nil
		return err
	}
	return nil
}

// AppendNDJSON appends new NDJSON records to an existing sealed store,
// producing a new store image written to w. Existing records are copied
// verbatim (their bytes and indexes are not rebuilt); only the new records are
// validated and indexed, and the offset table is extended. This is the
// incremental write path: O(new records) work plus a copy of the prior payload.
//
// prior is the existing store image (e.g. read from disk); r supplies the new
// lines. Returns the total record count after appending.
func AppendNDJSON(w io.Writer, prior []byte, r io.Reader) (int, error) {
	ps, err := parseNDJSON(prior)
	if err != nil {
		return 0, err
	}
	oldN := ps.n
	oldData := ps.data

	// Collect new records.
	var add bytes.Buffer
	var newOff []uint64
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 256<<10), 64<<20)
	addN := 0
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if !Valid(line) {
			return 0, ErrMalformedJSON
		}
		add.Write(line)
		add.WriteByte('\n')
		newOff = append(newOff, uint64(add.Len()))
		addN++
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	if addN == 0 {
		// Nothing to add: emit the prior image unchanged.
		_, err := w.Write(prior)
		return oldN, err
	}
	addData := add.Bytes()
	totalN := oldN + addN
	totalDataLen := uint64(len(oldData)) + uint64(len(addData))

	// Header (preserve the field-index flag; the section is rebuilt below).
	priorFlags := binary.LittleEndian.Uint32(prior[12:16])
	var hdr [ndjsonHeader]byte
	copy(hdr[0:8], ndjsonMagic)
	binary.LittleEndian.PutUint32(hdr[8:12], ndjsonVersion)
	binary.LittleEndian.PutUint32(hdr[12:16], priorFlags)
	binary.LittleEndian.PutUint64(hdr[16:24], uint64(totalN))
	binary.LittleEndian.PutUint64(hdr[24:32], totalDataLen)
	if _, err := w.Write(hdr[:]); err != nil {
		return 0, err
	}
	// Payload: old verbatim + new.
	if _, err := w.Write(oldData); err != nil {
		return 0, err
	}
	if _, err := w.Write(addData); err != nil {
		return 0, err
	}
	if pad := (4 - totalDataLen%4) % 4; pad > 0 {
		if _, err := w.Write(make([]byte, pad)); err != nil {
			return 0, err
		}
	}
	// Extended offset table: old offsets, then new shifted by oldData length.
	offsets := make([]uint64, totalN+1)
	copy(offsets, ps.offsets)
	base := uint64(len(oldData))
	for i := 0; i < addN; i++ {
		offsets[oldN+1+i] = base + newOff[i]
	}
	if err := writeU64s(w, offsets); err != nil {
		return 0, err
	}
	// Old per-record indexes copied verbatim, up to the precise end of the
	// record-index region (before any field-index section).
	if oldN > 0 {
		if _, err := w.Write(prior[ps.idxOff[0]:ps.recIdxEnd]); err != nil {
			return 0, err
		}
	}
	// New indexes built over the new record slices; kept for field-index rebuild.
	newDocs := make([]*Document, addN)
	for i := 0; i < addN; i++ {
		rec := bytes.TrimRight(addData[newOffStart(newOff, i):newOff[i]], "\n")
		d := IndexTape(rec)
		if d.oversize {
			return 0, ErrBadIndex
		}
		if err := d.Persist(w); err != nil {
			return 0, err
		}
		newDocs[i] = d
	}
	// Rebuild the field-index section over all records when the prior had one.
	if priorFlags&ndjsonFlagFieldIndex != 0 {
		fieldPaths := make([]string, len(ps.fields))
		for i, f := range ps.fields {
			fieldPaths[i] = f.path
		}
		allDocs := make([]*Document, totalN)
		var oldDoc Document
		for i := 0; i < oldN; i++ {
			rec := bytes.TrimRight(oldData[ps.offsets[i]:ps.offsets[i+1]], "\n")
			start := ps.idxOff[i]
			var end uint64
			if i+1 < oldN {
				end = ps.idxOff[i+1]
			} else {
				end = ps.recIdxEnd
			}
			if _, err := ViewInto(&oldDoc, prior[start:end], rec); err != nil {
				return 0, err
			}
			// Copy the document so each slice element is independent.
			cp := oldDoc
			allDocs[i] = &cp
		}
		copy(allDocs[oldN:], newDocs)
		if err := writeFieldIndex(w, allDocs, fieldPaths); err != nil {
			return 0, err
		}
	}
	return totalN, nil
}

func newOffStart(off []uint64, i int) uint64 {
	if i == 0 {
		return 0
	}
	return off[i-1]
}
