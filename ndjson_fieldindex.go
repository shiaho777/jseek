package jseek

import (
	"bytes"
	"encoding/binary"
	"io"
	"sort"
)

// Field index — lets a WHERE query skip non-matching records instead of scanning
// every one. For each indexed field the store keeps a sorted table of
// (value, recordID) entries; a comparison resolves to a contiguous record-ID set
// by binary search, and multiple predicates intersect their sets. Only the
// surviving candidates are then evaluated against the full predicate.
//
// This is the difference between "a fast scan" and "an index": on a selective
// query (e.g. status == "error" matching 1% of records) the engine touches ~1% of
// the records, not 100%. On a non-selective query it falls back to a scan, so it
// never does worse than the scan engine by more than the index lookup overhead.
//
// Section layout (little-endian), appended after the per-record indexes:
//
//	magic     [8]byte "JSEEKFI1"
//	nFields   uint32
//	per field:
//	  pathLen   uint16
//	  path      [pathLen]byte    (the dotted field path, UTF-8)
//	  nEntries  uint64
//	  entries   nEntries × {
//	              vtype   uint8     (Number/String/Boolean/Null)
//	              vlen    uint32
//	              value   [vlen]byte (Number: raw token; String: decoded bytes;
//	                                  Boolean: "true"/"false"; Null: empty)
//	              recID   uint64
//	            }  — sorted by (vtype, value, recID)
//
// Entries for records missing the field are omitted. The section is optional
// (header flag bit0); stores without it parse and query exactly as before.

const fieldIndexMagic = "JSEEKFI1"

// fiEntry is one (value, record) pair in a field index.
type fiEntry struct {
	vtype ValueType
	value []byte // canonical comparable bytes (see fiValue)
	rec   uint64
}

// fieldIndex is the parsed, in-memory form of one field's sorted table.
type fieldIndex struct {
	path    string
	entries []fiEntry // sorted by (vtype, value, rec)
}

// fiValue renders an extracted value into canonical comparable bytes. Numbers
// keep their raw token but are compared numerically at query time; strings are
// stored decoded so lexicographic order matches decoded order.
func fiValue(v []byte, vt ValueType) []byte {
	if vt == String {
		return unescapeInto(nil, v)
	}
	return v
}

// fiLess orders entries by type, then value, then record id. Numbers order
// numerically; other types bytewise.
func fiLess(a, b fiEntry) bool {
	if a.vtype != b.vtype {
		return a.vtype < b.vtype
	}
	if a.vtype == Number {
		af, ae := parseFloat(a.value)
		bf, be := parseFloat(b.value)
		if ae == nil && be == nil && af != bf {
			return af < bf
		}
	}
	if c := bytes.Compare(a.value, b.value); c != 0 {
		return c < 0
	}
	return a.rec < b.rec
}

// writeFieldIndex builds and writes the field-index section over the per-record
// documents. docs[i] covers record i.
func writeFieldIndex(w io.Writer, docs []*Document, fields []string) error {
	var hdr [12]byte
	copy(hdr[0:8], fieldIndexMagic)
	binary.LittleEndian.PutUint32(hdr[8:12], uint32(len(fields)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	for _, f := range fields {
		path := ParseDotPath(f)
		var entries []fiEntry
		for i, d := range docs {
			v, vt, _, err := d.Get(path...)
			if err != nil {
				continue // field absent in this record
			}
			entries = append(entries, fiEntry{vtype: vt, value: fiValue(v, vt), rec: uint64(i)})
		}
		sort.Slice(entries, func(i, j int) bool { return fiLess(entries[i], entries[j]) })
		// Field header.
		var fh [10]byte
		binary.LittleEndian.PutUint16(fh[0:2], uint16(len(f)))
		binary.LittleEndian.PutUint64(fh[2:10], uint64(len(entries)))
		if _, err := w.Write(fh[:]); err != nil {
			return err
		}
		if _, err := io.WriteString(w, f); err != nil {
			return err
		}
		for _, e := range entries {
			var eh [13]byte
			eh[0] = byte(e.vtype)
			binary.LittleEndian.PutUint32(eh[1:5], uint32(len(e.value)))
			binary.LittleEndian.PutUint64(eh[5:13], e.rec)
			if _, err := w.Write(eh[:]); err != nil {
				return err
			}
			if _, err := w.Write(e.value); err != nil {
				return err
			}
		}
	}
	return nil
}

// parseFieldIndex parses the trailing field-index section starting at img[pos:].
// It returns the indexes and the byte position just past the section.
func parseFieldIndex(img []byte, pos uint64) ([]fieldIndex, uint64, error) {
	if pos+12 > uint64(len(img)) || string(img[pos:pos+8]) != fieldIndexMagic {
		return nil, 0, ErrBadIndex
	}
	nf := binary.LittleEndian.Uint32(img[pos+8 : pos+12])
	pos += 12
	out := make([]fieldIndex, 0, nf)
	for fi := uint32(0); fi < nf; fi++ {
		if pos+10 > uint64(len(img)) {
			return nil, 0, ErrBadIndex
		}
		pl := uint64(binary.LittleEndian.Uint16(img[pos : pos+2]))
		ne := binary.LittleEndian.Uint64(img[pos+2 : pos+10])
		pos += 10
		if pos+pl > uint64(len(img)) {
			return nil, 0, ErrBadIndex
		}
		path := string(img[pos : pos+pl])
		pos += pl
		entries := make([]fiEntry, ne)
		for i := uint64(0); i < ne; i++ {
			if pos+13 > uint64(len(img)) {
				return nil, 0, ErrBadIndex
			}
			vt := ValueType(img[pos])
			vl := uint64(binary.LittleEndian.Uint32(img[pos+1 : pos+5]))
			rec := binary.LittleEndian.Uint64(img[pos+5 : pos+13])
			pos += 13
			if pos+vl > uint64(len(img)) {
				return nil, 0, ErrBadIndex
			}
			entries[i] = fiEntry{vtype: vt, value: img[pos : pos+vl], rec: rec}
			pos += vl
		}
		out = append(out, fieldIndex{path: path, entries: entries})
	}
	return out, pos, nil
}

// lookup returns the field index for a dotted path, or nil.
func (s *NDJSONStore) lookupField(path string) *fieldIndex {
	for i := range s.fields {
		if s.fields[i].path == path {
			return &s.fields[i]
		}
	}
	return nil
}

// candidateRange returns the record IDs whose indexed value satisfies op against
// the literal want (raw JSON). Because entries are sorted by (type, value), the
// matching set for an ordering op is contiguous within the literal's type band.
// ok is false when the field is not indexed or the literal type is absent.
func (f *fieldIndex) candidateRange(op string, want []byte) ([]uint64, bool) {
	wt := classify(want, 0)
	var wv []byte
	if wt == String && len(want) >= 2 {
		wv = unescapeInto(nil, want[1:len(want)-1])
	} else {
		wv = want
	}
	// Find the type band [lo, hi).
	lo := sort.Search(len(f.entries), func(i int) bool { return f.entries[i].vtype >= wt })
	hi := sort.Search(len(f.entries), func(i int) bool { return f.entries[i].vtype > wt })
	if lo >= hi {
		return nil, true // type absent: empty candidate set (valid)
	}
	band := f.entries[lo:hi]
	// Within the band, find the value boundary for the op.
	cmp := func(e fiEntry) int { return fiCmpValue(e, wt, wv) }
	var start, end int
	switch op {
	case "==":
		start = sort.Search(len(band), func(i int) bool { return cmp(band[i]) >= 0 })
		end = sort.Search(len(band), func(i int) bool { return cmp(band[i]) > 0 })
	case "!=":
		// Two ranges: everything below and everything above the equal band.
		eqStart := sort.Search(len(band), func(i int) bool { return cmp(band[i]) >= 0 })
		eqEnd := sort.Search(len(band), func(i int) bool { return cmp(band[i]) > 0 })
		out := make([]uint64, 0, len(band)-(eqEnd-eqStart))
		for _, e := range band[:eqStart] {
			out = append(out, e.rec)
		}
		for _, e := range band[eqEnd:] {
			out = append(out, e.rec)
		}
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		return out, true
	case ">":
		start = sort.Search(len(band), func(i int) bool { return cmp(band[i]) > 0 })
		end = len(band)
	case ">=":
		start = sort.Search(len(band), func(i int) bool { return cmp(band[i]) >= 0 })
		end = len(band)
	case "<":
		start = 0
		end = sort.Search(len(band), func(i int) bool { return cmp(band[i]) >= 0 })
	case "<=":
		start = 0
		end = sort.Search(len(band), func(i int) bool { return cmp(band[i]) > 0 })
	default:
		return nil, false
	}
	out := make([]uint64, end-start)
	for i, e := range band[start:end] {
		out[i] = e.rec
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, true
}

// fiCmpValue compares an entry's value against the query literal within the same
// type band: -1 if entry < literal, 0 equal, +1 greater.
func fiCmpValue(e fiEntry, wt ValueType, wv []byte) int {
	if wt == Number && e.vtype == Number {
		af, ae := parseFloat(e.value)
		bf, be := parseFloat(wv)
		if ae == nil && be == nil {
			switch {
			case af < bf:
				return -1
			case af > bf:
				return 1
			default:
				return 0
			}
		}
	}
	return bytes.Compare(e.value, wv)
}
