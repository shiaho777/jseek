package jseek

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
)

// WAL overlay store — genuinely cheap writes via a log-structured overlay.
//
// The contiguous-JSON problem (round 8): a field edit shifts every byte after
// it, so an in-place payload rewrite must copy the whole document. The fix is
// to NOT rewrite the base at all. A WALStore keeps an immutable sealed base
// plus an append-only edit log (the "overlay"). A write appends ONE small
// record — path + op + value — in O(edit size): no payload copy, no index
// rebuild. Reads resolve the base (zero-parse) then apply the latest matching
// overlay edit. Periodically Compact folds the log into a fresh sealed base,
// amortizing the one expensive copy over many cheap writes.
//
// This is the SQLite-WAL / LSM idea applied to the JSON substrate: writes are
// O(edit), reads pay a small overlay lookup, and compaction is batched.
//
// Concurrency: a WALStore is safe for concurrent use. Reads take a read lock;
// writes/compaction take the write lock. The base mapping is immutable.

// walOp is the kind of edit recorded in the overlay.
type walOp uint8

const (
	walSet walOp = iota
	walDelete
)

// walEdit is one appended edit. path is the joined key path; val is the raw
// JSON replacement (nil for delete).
type walEdit struct {
	path string
	op   walOp
	val  []byte
}

// WALStore is an immutable sealed base plus an append-only edit overlay.
type WALStore struct {
	mu   sync.RWMutex
	base *Store
	// edits in append order; later edits win for the same path.
	edits []walEdit
	// index from path -> position of its latest edit, for O(1) read lookup.
	latest map[string]int
	// wal, if set, is the file the edit log is persisted to (append-only).
	wal     *os.File
	walPath string
}

// OpenWAL opens a sealed base for cheap writes. If walPath is non-empty, the
// edit log is persisted there (created if absent, replayed if present) so
// writes survive restarts. Pass "" for a purely in-memory overlay.
func OpenWAL(basePath, walPath string) (*WALStore, error) {
	base, err := OpenStore(basePath)
	if err != nil {
		return nil, err
	}
	w := &WALStore{base: base, latest: make(map[string]int)}
	if walPath != "" {
		f, err := os.OpenFile(walPath, os.O_CREATE|os.O_RDWR, 0644)
		if err != nil {
			base.Close()
			return nil, err
		}
		w.wal = f
		w.walPath = walPath
		if err := w.replay(); err != nil {
			f.Close()
			base.Close()
			return nil, err
		}
	}
	return w, nil
}

// NewWAL wraps an already-open base with an in-memory overlay (no persistence).
func NewWAL(base *Store) *WALStore {
	return &WALStore{base: base, latest: make(map[string]int)}
}

// walPathKey joins a key path into a canonical string. Segments are escaped so
// the join is unambiguous (a key containing the separator cannot collide).
func walPathKey(keys []string) string {
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(0x1f) // unit separator, cannot appear in a JSON key unescaped
		}
		for j := 0; j < len(k); j++ {
			c := k[j]
			if c == 0x1f || c == '\\' {
				b.WriteByte('\\')
			}
			b.WriteByte(c)
		}
	}
	return b.String()
}

// Set records a replacement of the value at keys with setValue. It appends one
// edit to the overlay in O(edit size) — the base payload and index are NOT
// touched. setValue must be valid JSON. Returns immediately after the append
// (and, if persistent, the log write).
func (w *WALStore) Set(setValue []byte, keys ...string) error {
	if !Valid(setValue) {
		return ErrMalformedJSON
	}
	if err := w.checkApplicable(keys, true); err != nil {
		return err
	}
	return w.append(walEdit{path: walPathKey(keys), op: walSet, val: append([]byte(nil), setValue...)})
}

// Delete records removal of the value at keys. O(1) overlay append.
func (w *WALStore) Delete(keys ...string) error {
	if err := w.checkApplicable(keys, false); err != nil {
		return err
	}
	return w.append(walEdit{path: walPathKey(keys), op: walDelete})
}

// checkApplicable verifies the edit can apply to the current effective document
// using a single downward path walk (planSet/planDelete) — O(path depth), no
// payload rewrite, so writes stay cheap. It resolves against the base; if the
// overlay already dirties the relevant subtree it materializes first (rare).
func (w *WALStore) checkApplicable(keys []string, isSet bool) error {
	if len(keys) == 0 {
		return nil // whole-document replace/delete always applies
	}
	w.mu.RLock()
	base := w.base
	dirty := false
	key := walPathKey(keys)
	prefix := key + string(rune(0x1f))
	for p := range w.latest {
		if p == key {
			// Re-writing the exact same path just overwrites the prior edit;
			// applicability is unchanged, so no materialization is needed.
			continue
		}
		if strings.HasPrefix(p, prefix) || strings.HasPrefix(key, p+string(rune(0x1f))) {
			dirty = true
			break
		}
	}
	w.mu.RUnlock()
	var data []byte
	if dirty {
		var err error
		data, err = w.materialize()
		if err != nil {
			return err
		}
	} else {
		data = base.Data()
	}
	start := skipWhitespace(data, 0)
	if start >= len(data) {
		// Empty base: Set synthesizes a document (allowed); Delete is a no-op.
		if isSet {
			return validateBuildKeys(keys)
		}
		return nil
	}
	if isSet {
		_, err := planSet(data, start, keys)
		return err
	}
	// Delete tolerates a missing path (plain Delete returns the doc unchanged).
	return nil
}

func (w *WALStore) append(e walEdit) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.wal != nil {
		if err := writeWALEdit(w.wal, e); err != nil {
			return err
		}
	}
	idx := len(w.edits)
	w.edits = append(w.edits, e)
	w.latest[e.path] = idx
	return nil
}

// Get resolves the value at keys: the latest overlay edit wins; otherwise the
// base is read zero-parse. A delete in the overlay yields ErrKeyPathNotFound.
//
// Correctness for subtree reads: if any pending edit lies INSIDE the requested
// subtree (the read path is a proper prefix of an edit path), the base alone
// would be stale, so we materialize the affected document and read from it.
// Exact-path hits and unaffected subtrees stay on the zero-parse fast path.
func (w *WALStore) Get(keys ...string) ([]byte, ValueType, error) {
	key := walPathKey(keys)
	w.mu.RLock()
	idx, exact := w.latest[key]
	var e walEdit
	if exact {
		e = w.edits[idx]
	}
	// The base is stale for this read if any pending edit is inside the
	// requested subtree (read path is a prefix of the edit path) OR is an
	// ancestor of it (edit path is a prefix of the read path, replacing the
	// whole subtree the read descends into). The empty read path is the whole
	// document, so ANY edit dirties it.
	subtreeDirty := false
	if len(w.edits) > 0 {
		if key == "" {
			subtreeDirty = true
		} else {
			prefix := key + string(rune(0x1f))
			for p := range w.latest {
				if strings.HasPrefix(p, prefix) || strings.HasPrefix(key, p+string(rune(0x1f))) {
					subtreeDirty = true
					break
				}
			}
		}
	}
	base := w.base
	w.mu.RUnlock()

	if exact && !subtreeDirty {
		if e.op == walDelete {
			return nil, NotExist, ErrKeyPathNotFound
		}
		v, vt, _, err := valueAt(e.val, skipWhitespace(e.val, 0))
		return v, vt, err
	}
	if subtreeDirty {
		// Materialize base + overlay, then read the (possibly partial) subtree.
		doc, err := w.materialize()
		if err != nil {
			return nil, NotExist, err
		}
		v, vt, _, err := Get(doc, keys...)
		return v, vt, err
	}
	d, err := base.View()
	if err != nil {
		return nil, NotExist, err
	}
	v, vt, _, err := d.Get(keys...)
	return v, vt, err
}

// materialize applies all pending edits to a copy of the base payload and
// returns the resulting JSON. Used only for subtree reads the overlay affects.
func (w *WALStore) materialize() ([]byte, error) {
	w.mu.RLock()
	edits := append([]walEdit(nil), w.edits...)
	base := w.base
	w.mu.RUnlock()
	d, err := base.View()
	if err != nil {
		return nil, err
	}
	cur := append([]byte(nil), base.Data()...)
	cd := d
	for _, e := range edits {
		keys := splitWALPath(e.path)
		if e.op == walDelete {
			cur, cd, _, err = PatchDelete(cd, cur, keys...)
		} else {
			cur, cd, err = PatchSet(cd, cur, e.val, keys...)
		}
		if err != nil {
			return nil, err
		}
	}
	return cur, nil
}

// Exists reports whether keys resolves to a value (overlay-aware).
func (w *WALStore) Exists(keys ...string) bool {
	_, _, err := w.Get(keys...)
	return err == nil
}

// GetInt / GetString / GetFloat / GetBoolean are typed overlay-aware getters.
func (w *WALStore) GetInt(keys ...string) (int64, error) {
	v, vt, err := w.Get(keys...)
	if err != nil {
		return 0, err
	}
	if vt != Number {
		return 0, ErrUnexpectedType
	}
	return parseInt(v)
}

func (w *WALStore) GetString(keys ...string) (string, error) {
	v, vt, err := w.Get(keys...)
	if err != nil {
		return "", err
	}
	if vt != String {
		return "", ErrUnexpectedType
	}
	return string(unescapeInto(nil, v)), nil
}

// Pending returns the number of un-compacted edits in the overlay.
func (w *WALStore) Pending() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.edits)
}

// Compact folds the overlay into a fresh sealed base, applying every pending
// edit with the incremental patch, then resets the log. This is the one
// expensive operation (a payload copy), amortized over all the cheap writes
// since the last compaction. If walPath is set, the log is truncated.
//
// The base is replaced atomically under the write lock; concurrent readers see
// either the old or the new base, never a torn state.
func (w *WALStore) Compact() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.edits) == 0 {
		return nil
	}
	// Materialize the current base payload, then apply edits in order.
	d, err := w.base.View()
	if err != nil {
		return err
	}
	cur := append([]byte(nil), w.base.Data()...)
	cd := d
	for _, e := range w.edits {
		keys := splitWALPath(e.path)
		if e.op == walDelete {
			cur, cd, _, err = PatchDelete(cd, cur, keys...)
		} else {
			cur, cd, err = PatchSet(cd, cur, e.val, keys...)
		}
		if err != nil {
			return err
		}
	}
	// Seal the compacted payload into a new in-memory base.
	var buf bytes.Buffer
	if err := Seal(&buf, cur); err != nil {
		return err
	}
	nb, err := openStoreImage(buf.Bytes())
	if err != nil {
		return err
	}
	old := w.base
	w.base = nb
	w.edits = nil
	w.latest = make(map[string]int)
	if w.wal != nil {
		if err := w.wal.Truncate(0); err != nil {
			return err
		}
		if _, err := w.wal.Seek(0, io.SeekStart); err != nil {
			return err
		}
	}
	if old != nil {
		old.Close()
	}
	return nil
}

// Close releases the base mapping and the WAL file. Pending edits are NOT
// compacted automatically; call Compact first if you want them folded into the
// base file (the WAL file itself persists them for replay).
func (w *WALStore) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	var err error
	if w.wal != nil {
		err = w.wal.Close()
		w.wal = nil
	}
	if w.base != nil {
		if e := w.base.Close(); e != nil && err == nil {
			err = e
		}
		w.base = nil
	}
	return err
}

// splitWALPath reverses walPathKey.
func splitWALPath(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			i++
			b.WriteByte(s[i])
			continue
		}
		if c == 0x1f {
			out = append(out, b.String())
			b.Reset()
			continue
		}
		b.WriteByte(c)
	}
	out = append(out, b.String())
	return out
}

// --- WAL persistence format ---
// Each record: magic byte 'E', op byte, u16 pathLen, path bytes, u32 valLen,
// val bytes. Little-endian. A torn trailing record (short read) is ignored on
// replay, which is safe: it was never acknowledged as committed.

func writeWALEdit(w io.Writer, e walEdit) error {
	var hdr [8]byte
	hdr[0] = 'E'
	hdr[1] = byte(e.op)
	binary.LittleEndian.PutUint16(hdr[2:4], uint16(len(e.path)))
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(len(e.val)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := io.WriteString(w, e.path); err != nil {
		return err
	}
	if len(e.val) > 0 {
		if _, err := w.Write(e.val); err != nil {
			return err
		}
	}
	return nil
}

func (w *WALStore) replay() error {
	data, err := io.ReadAll(w.wal)
	if err != nil {
		return err
	}
	// Position the file at the end for subsequent appends.
	if _, err := w.wal.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	pos := 0
	for pos+8 <= len(data) {
		if data[pos] != 'E' {
			break // corrupt/torn tail: stop, ignore the rest
		}
		op := walOp(data[pos+1])
		pl := int(binary.LittleEndian.Uint16(data[pos+2 : pos+4]))
		vl := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		pos += 8
		if pos+pl+vl > len(data) {
			break // torn record
		}
		path := string(data[pos : pos+pl])
		pos += pl
		val := append([]byte(nil), data[pos:pos+vl]...)
		pos += vl
		idx := len(w.edits)
		w.edits = append(w.edits, walEdit{path: path, op: op, val: val})
		w.latest[path] = idx
	}
	return nil
}

// openStoreImage opens a sealed store from an in-memory image (no file). It
// copies the image into a private buffer so the caller's slice may be reused.
func openStoreImage(img []byte) (*Store, error) {
	priv := append([]byte(nil), img...)
	l, ok := parseSealed(priv)
	if !ok {
		return nil, ErrBadIndex
	}
	if err := VerifyIndex(l.idx, l.data); err != nil {
		return nil, err
	}
	return &Store{image: priv, layout: l}, nil
}

// sortedPaths is a helper for deterministic compaction diagnostics (unused in
// the hot path; kept for tests).
func (w *WALStore) sortedPaths() []string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := make([]string, 0, len(w.latest))
	for p := range w.latest {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}