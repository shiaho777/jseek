package jseek

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
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
	// basePath, if set, is the sealed base file. Compact persists the folded
	// base here atomically before clearing the log, so a crash never loses
	// committed edits.
	basePath string
	// gen bumps on every mutation (append/compact/replay). matGen records the
	// generation matDoc was built at; a dirty-subtree read reuses matDoc when
	// matGen == gen, making repeated reads O(1) instead of O(document).
	gen    uint64
	matMu  sync.Mutex
	matGen uint64
	matDoc []byte
	matErr error
	// lock, if set, is the cross-process write lock (held for the WALStore's
	// lifetime). Only one process may write a given WAL at a time.
	lock *fileLock
}

// OpenWAL opens a sealed base for cheap writes. If walPath is non-empty, the
// edit log is persisted there (created if absent, replayed if present) so
// writes survive restarts. Pass "" for a purely in-memory overlay.
func OpenWAL(basePath, walPath string) (*WALStore, error) {
	base, err := OpenStore(basePath)
	if err != nil {
		return nil, err
	}
	w := &WALStore{base: base, latest: make(map[string]int), basePath: basePath}
	if walPath != "" {
		// Coordinate writers across processes: take an exclusive lock on a
		// sidecar before touching the log. A second writer gets ErrLocked.
		lk, err := acquireLock(walPath + ".lock")
		if err != nil {
			base.Close()
			return nil, err
		}
		w.lock = lk
		f, err := os.OpenFile(walPath, os.O_CREATE|os.O_RDWR, 0644)
		if err != nil {
			lk.release()
			base.Close()
			return nil, err
		}
		w.wal = f
		w.walPath = walPath
		if err := w.replay(); err != nil {
			f.Close()
			lk.release()
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
	if dirty {
		// Rare: the overlay already changed this subtree, so validate against
		// the materialized document with the full planner.
		data, err := w.materialize()
		if err != nil {
			return err
		}
		start := skipWhitespace(data, 0)
		if start >= len(data) {
			if isSet {
				return validateBuildKeys(keys)
			}
			return nil
		}
		if isSet {
			_, err := planSet(data, start, keys)
			return err
		}
		return nil
	}
	// Fast path: validate against the base using the structural index, so a
	// deep array index is located by index stride — not an O(n) byte scan.
	if !isSet {
		return nil // Delete tolerates a missing path.
	}
	bd := base.Data()
	if len(skipWhitespaceSlice(bd)) == 0 {
		return validateBuildKeys(keys) // empty base: Set synthesizes objects
	}
	d, err := base.View()
	if err != nil {
		return err
	}
	// If the full path resolves via the index, the edit replaces an existing
	// value — always applicable. This uses indexed array strides (O(1)-ish).
	if _, ok := d.seekIndexed(keys); ok {
		return nil
	}
	// Path absent: find the deepest existing ancestor via the index, then the
	// remaining (to-be-created) segments must be object keys only, and the
	// ancestor must be an object (arrays are not extended, scalars cannot be
	// descended). Walking prefixes uses indexed navigation, not byte scans.
	existing := 0
	for i := 1; i <= len(keys); i++ {
		if _, ok := d.seekIndexed(keys[:i]); ok {
			existing = i
		} else {
			break
		}
	}
	if existing == 0 {
		// No prefix resolves. If the root is a non-empty scalar, Set cannot
		// descend into it (matches planSet's ErrUnexpectedType). Only an empty
		// base allows synthesizing a fresh object document.
		rstart := skipWhitespace(bd, 0)
		if rstart < len(bd) && bd[rstart] != '{' {
			return ErrUnexpectedType
		}
		return validateBuildKeys(keys)
	}
	astart, _ := d.seekIndexed(keys[:existing])
	if astart >= len(bd) || bd[astart] != '{' {
		return ErrKeyPathNotFound // cannot create under an array/scalar
	}
	return validateBuildKeys(keys[existing:])
}

// skipWhitespaceSlice returns data with leading JSON whitespace removed.
func skipWhitespaceSlice(data []byte) []byte {
	i := skipWhitespace(data, 0)
	return data[i:]
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
	w.gen++
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
		// Subtree-local resolution: touch only the requested subtree and the
		// edits inside it — never the whole document. This breaks the
		// "first dirty read is O(document)" boundary.
		return w.resolveSubtree(keys)
	}
	d, err := base.View()
	if err != nil {
		return nil, NotExist, err
	}
	v, vt, _, err := d.Get(keys...)
	return v, vt, err
}

// resolveSubtree reads keys by combining the nearest ancestor source (an
// ancestor overlay edit's value, or the base subtree) with only the descendant
// edits that fall inside the requested subtree. Cost is O(subtree size +
// edits-in-subtree), independent of the rest of the document.
func (w *WALStore) resolveSubtree(keys []string) ([]byte, ValueType, error) {
	key := walPathKey(keys)
	sep := string(rune(0x1f))
	w.mu.RLock()
	// Find the nearest ancestor edit (proper prefix of key) and collect
	// descendant edits (key is a proper prefix of them), plus an exact hit.
	ancestorPath := ""
	ancestorIdx := -1
	var desc []walEdit
	var exactEdit *walEdit
	for p, i := range w.latest {
		e := w.edits[i]
		switch {
		case p == key:
			ee := e
			exactEdit = &ee
		case key != "" && strings.HasPrefix(key, p+sep):
			// p is a proper ancestor of key; keep the longest (nearest).
			if len(p) > len(ancestorPath) {
				ancestorPath = p
				ancestorIdx = i
			}
		case key == "" || strings.HasPrefix(p, key+sep):
			// p is a proper descendant of key (or key is the root, so every
			// edit is a descendant).
			desc = append(desc, e)
		}
	}
	base := w.base
	w.mu.RUnlock()

	// Determine the source bytes for the subtree rooted at `keys` and the
	// relative path from that source to `keys`. An exact edit at `keys`
	// replaces the whole subtree and takes precedence over base/ancestor.
	var src []byte
	var relKeys []string
	switch {
	case exactEdit != nil:
		if exactEdit.op == walDelete {
			return nil, NotExist, ErrKeyPathNotFound
		}
		src = exactEdit.val
		relKeys = nil
	case ancestorIdx >= 0:
		ae := w.edits[ancestorIdx]
		if ae.op == walDelete {
			return nil, NotExist, ErrKeyPathNotFound
		}
		src = ae.val
		relKeys = keys[len(splitWALPath(ancestorPath)):]
	default:
		bd := base.Data()
		// Locate the subtree via the structural index (O(index navigation)),
		// not a stateless byte scan — so locating a deep element does not cost
		// O(array length).
		d, err := base.View()
		if err != nil {
			return nil, NotExist, err
		}
		start, ok := d.seekIndexed(keys)
		if !ok {
			return nil, NotExist, ErrKeyPathNotFound
		}
		vs, ve, vt, vok := valueBounds(bd, start)
		if !vok {
			return nil, NotExist, ErrMalformedJSON
		}
		if vt == String {
			ve++ // include closing quote for a self-contained token
			vs-- // include opening quote
		}
		src = bd[vs:ve]
		relKeys = nil
	}

	// If there are no descendant edits, the source already answers the read.
	if len(desc) == 0 {
		v, vt, _, err := Get(src, relKeys...)
		return v, vt, err
	}

	// Apply descendant edits relative to the source root. Sort by path depth
	// (shallow first) so ancestor edits land before deeper ones; PatchSet
	// handles ordering, but shallow-first avoids creating then overwriting.
	sort.Slice(desc, func(i, j int) bool {
		return strings.Count(desc[i].path, sep) < strings.Count(desc[j].path, sep)
	})
	cur := append([]byte(nil), src...)
	cd := Index(cur)
	// The source root corresponds to `keys` (or an ancestor). Descendant edit
	// paths are absolute; strip the prefix up to the source root.
	rootDepth := len(keys) - len(relKeys)
	for _, e := range desc {
		abs := splitWALPath(e.path)
		rel := abs[rootDepth:]
		var err error
		if e.op == walDelete {
			cur, cd, _, err = PatchDelete(cd, cur, rel...)
		} else {
			cur, cd, err = PatchSet(cd, cur, e.val, rel...)
		}
		if err != nil {
			return nil, NotExist, err
		}
	}
	v, vt, _, err := Get(cur, relKeys...)
	return v, vt, err
}

// materialize returns base + all pending edits as JSON. The result is cached
// against the edit generation: repeated dirty-subtree reads reuse it in O(1),
// and it is rebuilt only when a write/compact changes the edit set. This breaks
// the "every dirty read is O(document)" boundary for read-after-write workloads.
// The returned slice is shared and must be treated as read-only by callers.
func (w *WALStore) materialize() ([]byte, error) {
	w.mu.RLock()
	gen := w.gen
	edits := append([]walEdit(nil), w.edits...)
	base := w.base
	w.mu.RUnlock()

	w.matMu.Lock()
	defer w.matMu.Unlock()
	if w.matGen == gen && w.matDoc != nil {
		return w.matDoc, w.matErr
	}
	doc, err := buildMaterialized(base, edits)
	w.matGen, w.matDoc, w.matErr = gen, doc, err
	return doc, err
}

// buildMaterialized applies edits to a copy of the base payload.
func buildMaterialized(base *Store, edits []walEdit) ([]byte, error) {
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
	// Seal the compacted payload.
	var buf bytes.Buffer
	if err := Seal(&buf, cur); err != nil {
		return err
	}
	img := buf.Bytes()

	// Crash-safe ordering: persist the new base BEFORE clearing the log.
	//   - crash before the base rename: old base + full WAL replay correctly.
	//   - crash after the base rename but before WAL truncate: the new base
	//     already contains every edit; replaying the WAL re-applies them
	//     idempotently (same final values), so nothing is lost or doubled.
	//   - crash after WAL truncate: new base is durable and complete.
	if w.basePath != "" {
		if err := WriteFileAtomic(w.basePath, img, 0644); err != nil {
			return err
		}
	}
	nb, err := openStoreImage(img)
	if err != nil {
		return err
	}
	old := w.base
	w.base = nb
	w.edits = nil
	w.latest = make(map[string]int)
	w.gen++
	if w.wal != nil {
		if err := w.wal.Truncate(0); err != nil {
			return err
		}
		if err := w.wal.Sync(); err != nil {
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
	if w.lock != nil {
		if e := w.lock.release(); e != nil && err == nil {
			err = e
		}
		w.lock = nil
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

// --- WAL persistence format (v2, checksummed) ---
// Each record: magic byte 'E', op byte, u16 pathLen, u32 valLen, path bytes,
// val bytes, u32 CRC32 (IEEE, over op..val). Little-endian. The checksum lets
// replay distinguish a cleanly committed record from a torn/corrupt tail: a
// record whose CRC does not match (or that is truncated) was never acknowledged
// and is discarded, and the log is truncated back to the last good record so
// future appends stay consistent.
//
// Legacy v1 records (no trailing CRC) are still readable: replay detects them by
// the absence of a valid checksum and accepts them only when the record is
// complete and followed by another valid record or EOF. New writes always emit
// the checksummed v2 format.

const walCRCSize = 4

func walRecordBytes(e walEdit) []byte {
	n := 8 + len(e.path) + len(e.val) + walCRCSize
	buf := make([]byte, n)
	buf[0] = 'E'
	buf[1] = byte(e.op)
	binary.LittleEndian.PutUint16(buf[2:4], uint16(len(e.path)))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(e.val)))
	copy(buf[8:], e.path)
	copy(buf[8+len(e.path):], e.val)
	// CRC covers everything after the magic byte through the value.
	crc := crc32.ChecksumIEEE(buf[1 : n-walCRCSize])
	binary.LittleEndian.PutUint32(buf[n-walCRCSize:], crc)
	return buf
}

// writeWALEdit appends one checksummed record. When w is an *os.File the append
// is fsynced so a confirmed write is durable; for other writers (tests, buffers)
// it is a plain write.
func writeWALEdit(w io.Writer, e walEdit) error {
	rec := walRecordBytes(e)
	if f, ok := w.(*os.File); ok {
		return appendFileSync(f, rec)
	}
	_, err := w.Write(rec)
	return err
}

func (w *WALStore) replay() error {
	if _, err := w.wal.Seek(0, io.SeekStart); err != nil {
		return err
	}
	data, err := io.ReadAll(w.wal)
	if err != nil {
		return err
	}
	pos := 0
	good := 0 // byte offset just past the last fully validated record
	for pos+8 <= len(data) {
		if data[pos] != 'E' {
			break // corrupt/torn tail
		}
		op := walOp(data[pos+1])
		pl := int(binary.LittleEndian.Uint16(data[pos+2 : pos+4]))
		vl := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		bodyEnd := pos + 8 + pl + vl
		if bodyEnd > len(data) {
			break // truncated record
		}
		// v2: a trailing CRC32 must match. v1 (no CRC) is accepted only when the
		// record is complete and the next byte starts another record or EOF.
		if bodyEnd+walCRCSize <= len(data) {
			want := binary.LittleEndian.Uint32(data[bodyEnd : bodyEnd+walCRCSize])
			got := crc32.ChecksumIEEE(data[pos+1 : bodyEnd])
			if want != got {
				// Could be a legacy v1 record whose "CRC" bytes are actually the
				// next record's header. Accept as v1 only if the next byte is a
				// record magic or this is the final record.
				nextIsRecord := bodyEnd == len(data) || data[bodyEnd] == 'E'
				if !nextIsRecord {
					break // genuine corruption
				}
				// Legacy v1: consume without the CRC.
			} else {
				bodyEnd += walCRCSize
			}
		}
		path := string(data[pos+8 : pos+8+pl])
		val := append([]byte(nil), data[pos+8+pl:pos+8+pl+vl]...)
		idx := len(w.edits)
		w.edits = append(w.edits, walEdit{path: path, op: op, val: val})
		w.latest[path] = idx
		pos = bodyEnd
		good = pos
	}
	// Truncate any torn/corrupt tail so future appends extend a consistent log.
	if good < len(data) {
		if err := w.wal.Truncate(int64(good)); err != nil {
			return err
		}
		if err := w.wal.Sync(); err != nil {
			return err
		}
	}
	if _, err := w.wal.Seek(0, io.SeekEnd); err != nil {
		return err
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
