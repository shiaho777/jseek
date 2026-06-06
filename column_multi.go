package jseek

// Multi-column transposition: extract several fields from each record in a
// SINGLE index pass per record, instead of one pass per column. This is the
// efficient path when an analytics job needs many columns from the same batch
// (the usual case): the expensive Stage-1 index is built once per record and
// shared across all requested columns.

// Frame is the columnar result of transposing a batch: each requested path
// becomes one column of raw value slices, all aligned by record index. Raw
// values alias the records (zero copy); records must stay live.
type Frame struct {
	// Paths are the requested paths, in column order.
	Paths [][]string
	// Cols[c][r] is the raw value of column c in record r (quotes stripped for
	// strings), or nil if absent in that record.
	Cols [][][]byte
	// Types[c][r] is the value type of column c in record r.
	Types [][]ValueType
	// Rows is the number of records.
	Rows int
}

// Transpose extracts all paths from every record in one index pass per record,
// returning a column-oriented Frame. Each record is indexed once; per-column
// trajectories are cached and verified, falling back to search on drift.
func Transpose(records [][]byte, paths ...[]string) *Frame {
	f := &Frame{
		Paths: paths,
		Cols:  make([][][]byte, len(paths)),
		Types: make([][]ValueType, len(paths)),
		Rows:  len(records),
	}
	for c := range paths {
		f.Cols[c] = make([][]byte, len(records))
		f.Types[c] = make([]ValueType, len(records))
	}
	var d Document
	for r, rec := range records {
		d.Reset(rec)
		for c, path := range paths {
			v, vt, ok := readColumn(&d, path)
			if ok {
				f.Cols[c][r] = v
				f.Types[c][r] = vt
			} else {
				f.Types[c][r] = NotExist
			}
		}
	}
	return f
}

// readColumn reads path from the already-indexed document d via a forward
// search over the shared structural index, returning the value bytes (quotes
// stripped for strings), its type, and ok.
//
// It deliberately does NOT use a cross-record "trajectory" cache. Such a cache
// remembers a structural-index position learned on one record and replays it on
// another; under duplicate keys that position can align with a LATER duplicate
// that still passes a key check, so it returns a non-first occurrence while a
// fresh search (and jseek's documented contract) returns the first. A sound
// first-occurrence check for a flat object path is itself a forward search, so
// there is no correct shortcut to keep — we search, reusing the structural
// index the multi-column pass already built once per record.
func readColumn(d *Document, path []string) ([]byte, ValueType, bool) {
	start, ok := d.seekIndexed(path)
	if !ok {
		return nil, NotExist, false
	}
	vs, ve, vt, vok := valueBounds(d.data, start)
	if !vok {
		return nil, NotExist, false
	}
	if vt == String {
		return d.data[vs:ve], String, true
	}
	return d.data[vs:ve], vt, true
}

// Int decodes column c as int64 values, using missing for absent/non-int cells.
func (f *Frame) Int(c int, missing int64) []int64 {
	out := make([]int64, f.Rows)
	if c < 0 || c >= len(f.Cols) {
		for i := range out {
			out[i] = missing
		}
		return out
	}
	for r := 0; r < f.Rows; r++ {
		if f.Types[c][r] != Number {
			out[r] = missing
			continue
		}
		n, err := parseInt(f.Cols[c][r])
		if err != nil {
			out[r] = missing
			continue
		}
		out[r] = n
	}
	return out
}

// Float decodes column c as float64 values.
func (f *Frame) Float(c int, missing float64) []float64 {
	out := make([]float64, f.Rows)
	if c < 0 || c >= len(f.Cols) {
		for i := range out {
			out[i] = missing
		}
		return out
	}
	for r := 0; r < f.Rows; r++ {
		if f.Types[c][r] != Number {
			out[r] = missing
			continue
		}
		v, err := parseFloat(f.Cols[c][r])
		if err != nil {
			out[r] = missing
			continue
		}
		out[r] = v
	}
	return out
}

// Strings decodes column c as strings (escapes decoded).
func (f *Frame) Strings(c int, missing string) []string {
	out := make([]string, f.Rows)
	if c < 0 || c >= len(f.Cols) {
		for i := range out {
			out[i] = missing
		}
		return out
	}
	for r := 0; r < f.Rows; r++ {
		if f.Types[c][r] != String {
			out[r] = missing
			continue
		}
		out[r] = string(unescapeInto(nil, f.Cols[c][r]))
	}
	return out
}
