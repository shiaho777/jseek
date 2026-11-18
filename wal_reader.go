package jseek

import (
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
	"sync"
)

// WALReader is a read-only, cross-process view of a WAL store. It lets reader
// processes see a writer's committed edits without taking the write lock:
//
//   - the sealed base is memory-mapped and verified once;
//   - Refresh() re-maps the base only if the writer atomically replaced it
//     (detected by file identity/size), and incrementally replays newly appended
//     WAL records (tracking a byte offset, so it never re-reads the whole log);
//   - reads resolve the base then apply the latest matching overlay edit.
//
// A WALReader is safe for concurrent use. It never writes; the writer holds the
// exclusive lock separately. Because the base is replaced atomically and the WAL
// is append-only with checksummed records, a reader always sees a consistent
// snapshot — either before or after a compaction, never a torn mix.
type WALReader struct {
	mu      sync.RWMutex
	base    *Store
	walPath string
	edits   []walEdit
	latest  map[string]int
	walOff  int64 // byte offset up to which the WAL has been replayed
}

// OpenWALReader opens a read-only view over a sealed base and its WAL. The WAL
// file is optional ("" means base only). It replays the current WAL once.
func OpenWALReader(basePath, walPath string) (*WALReader, error) {
	base, err := OpenStore(basePath)
	if err != nil {
		return nil, err
	}
	r := &WALReader{base: base, walPath: walPath, latest: make(map[string]int)}
	if walPath != "" {
		if err := r.refreshWAL(); err != nil {
			base.Close()
			return nil, err
		}
	}
	return r, nil
}

// Refresh updates the view to the writer's latest committed state: it re-maps
// the base if it changed and replays any new WAL records. Call it periodically
// (or before a read that must be fresh). It is cheap when nothing changed.
func (r *WALReader) Refresh() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.refreshBase(); err != nil {
		return err
	}
	if r.walPath != "" {
		return r.refreshWAL()
	}
	return nil
}

// refreshBase re-maps the base file if the writer replaced it. File identity
// (same-file check) plus size detects an atomic rename replacement.
func (r *WALReader) refreshBase() error {
	if r.base == nil || r.base.path == "" {
		return nil
	}
	fi, err := os.Stat(r.base.path)
	if err != nil {
		return err
	}
	cur, err := r.base.f.Stat()
	if err != nil {
		return err
	}
	if os.SameFile(fi, cur) && fi.Size() == cur.Size() {
		return nil // unchanged
	}
	nb, err := OpenStore(r.base.path)
	if err != nil {
		return err
	}
	old := r.base
	r.base = nb
	// A new base means the writer compacted: the old overlay is folded in, so
	// drop it and replay the (truncated) WAL from the start.
	r.edits = nil
	r.latest = make(map[string]int)
	r.walOff = 0
	if old != nil {
		old.Close()
	}
	return nil
}

// refreshWAL replays records appended since walOff. A torn trailing record is
// left for the next Refresh (it was never acknowledged).
func (r *WALReader) refreshWAL() error {
	f, err := os.Open(r.walPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no WAL yet
		}
		return err
	}
	defer f.Close()
	if _, err := f.Seek(r.walOff, io.SeekStart); err != nil {
		return err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	pos := 0
	for pos+8 <= len(data) {
		if data[pos] != 'E' {
			break
		}
		op := walOp(data[pos+1])
		pl := int(binary.LittleEndian.Uint16(data[pos+2 : pos+4]))
		vl := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		bodyEnd := pos + 8 + pl + vl
		if bodyEnd+walCRCSize > len(data) {
			break // incomplete record: wait for the writer to finish
		}
		want := binary.LittleEndian.Uint32(data[bodyEnd : bodyEnd+walCRCSize])
		if crc32.ChecksumIEEE(data[pos+1:bodyEnd]) != want {
			break // corrupt/torn tail: stop, do not advance past it
		}
		path := string(data[pos+8 : pos+8+pl])
		val := append([]byte(nil), data[pos+8+pl:bodyEnd]...)
		idx := len(r.edits)
		r.edits = append(r.edits, walEdit{path: path, op: op, val: val})
		r.latest[path] = idx
		pos = bodyEnd + walCRCSize
	}
	r.walOff += int64(pos)
	return nil
}

// Get returns the effective raw value at keys, applying the latest overlay edit.
// It reads the base via the zero-parse index, then checks the overlay.
func (r *WALReader) Get(keys ...string) ([]byte, ValueType, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	key := walPathKey(keys)
	if idx, ok := r.latest[key]; ok {
		e := r.edits[idx]
		if e.op == walDelete {
			return nil, NotExist, ErrKeyPathNotFound
		}
		v, vt, _, err := Get(e.val)
		return v, vt, err
	}
	d, err := r.base.View()
	if err != nil {
		return nil, NotExist, err
	}
	v, vt, _, err := d.Get(keys...)
	return v, vt, err
}

// GetInt returns the effective integer value at keys.
func (r *WALReader) GetInt(keys ...string) (int64, error) {
	v, vt, err := r.Get(keys...)
	if err != nil {
		return 0, err
	}
	if vt != Number {
		return 0, ErrUnexpectedType
	}
	return parseInt(v)
}

// Pending returns the number of overlay edits currently visible.
func (r *WALReader) Pending() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.edits)
}

// Close releases the base mapping.
func (r *WALReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.base != nil {
		err := r.base.Close()
		r.base = nil
		return err
	}
	return nil
}
