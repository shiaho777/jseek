package jseek

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
)

// Self-contained sealed stores: one file holding payload + index, openable
// with zero re-parsing. This is the "hand someone a .jstore and they query it
// instantly" shape — the substrate as a portable artifact.
//
// Container layout (little-endian):
//
//	magic    [8]byte "JSEEKST1"
//	version  uint32  (1)
//	flags    uint32  (reserved, 0)
//	dataLen  uint64  payload length
//	idxLen   uint64  index length (header + arrays, as Persist emits)
//	--- header = 32 bytes ---
//	payload  [dataLen]byte
//	index    [idxLen]byte
//
// The payload sits at offset 32; the index at 32+dataLen. Both regions are
// 4-byte aligned when dataLen is a multiple of 4; ViewSealed does not require
// payload alignment (only the index arrays are reinterpreted, and the index
// region starts at 32+dataLen which the sealer pads to a multiple of 4).

const (
	storeMagic   = "JSEEKST1"
	storeVersion = 1
	storeHeader  = 32
)

// Seal writes data and its structural index into w as one self-contained
// store. The index is built with the skip tape (IndexTape) for fastest
// subsequent navigation. data is never mutated.
func Seal(w io.Writer, data []byte) error {
	d := IndexTape(data)
	if d.oversize {
		return ErrBadIndex
	}
	// Pad the payload region so the index arrays start 4-byte aligned.
	pad := (4 - len(data)%4) % 4
	var hdr [storeHeader]byte
	copy(hdr[0:8], storeMagic)
	binary.LittleEndian.PutUint32(hdr[8:12], storeVersion)
	binary.LittleEndian.PutUint32(hdr[12:16], 0)
	binary.LittleEndian.PutUint64(hdr[16:24], uint64(len(data)))
	binary.LittleEndian.PutUint64(hdr[24:32], uint64(d.IndexSize()))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	if pad > 0 {
		if _, err := w.Write(make([]byte, pad)); err != nil {
			return err
		}
	}
	return d.Persist(w)
}

// sealedLayout locates the payload and index regions inside a store image.
type sealedLayout struct {
	data []byte
	idx  []byte
}

func parseSealed(b []byte) (sealedLayout, bool) {
	if len(b) < storeHeader {
		return sealedLayout{}, false
	}
	if string(b[0:8]) != storeMagic {
		return sealedLayout{}, false
	}
	if binary.LittleEndian.Uint32(b[8:12]) != storeVersion {
		return sealedLayout{}, false
	}
	dataLen := binary.LittleEndian.Uint64(b[16:24])
	idxLen := binary.LittleEndian.Uint64(b[24:32])
	pad := (4 - dataLen%4) % 4
	idxOff := storeHeader + dataLen + pad
	end := idxOff + idxLen
	if idxOff > uint64(len(b)) || end > uint64(len(b)) || end < idxOff {
		return sealedLayout{}, false
	}
	return sealedLayout{
		data: b[storeHeader : storeHeader+dataLen],
		idx:  b[idxOff:end],
	}, true
}

// OpenSealed reads a self-contained store from r and returns a Document over
// its payload, verifying integrity. The payload is copied into memory (use
// ViewSealed over an mmap'd image for zero-copy).
func OpenSealed(r io.Reader) (*Document, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return ViewSealed(b)
}

// ViewSealed opens a self-contained store image (e.g. an mmap'd .jstore file)
// with zero copying: both payload and index alias b. It verifies the payload
// hash and index offsets. The returned Document aliases b — keep the mapping
// alive and immutable for the Document's lifetime.
func ViewSealed(b []byte) (*Document, error) {
	l, ok := parseSealed(b)
	if !ok {
		return nil, ErrBadIndex
	}
	return View(l.idx, l.data)
}

// ViewSealedTrusted is ViewSealed without the per-open payload hash (offset
// bounds still validated). Verify the image once with VerifySealed first.
func ViewSealedTrusted(b []byte) (*Document, error) {
	l, ok := parseSealed(b)
	if !ok {
		return nil, ErrBadIndex
	}
	return ViewTrusted(l.idx, l.data)
}

// VerifySealed checks a store image's integrity once; afterwards
// ViewSealedTrusted opens it in O(1).
func VerifySealed(b []byte) error {
	_, err := ViewSealed(b)
	return err
}

// Store is an mmap-backed sealed store opened for repeated zero-parse reads.
// It maps the file once, verifies it once, and hands out O(1) Documents via
// View. Call Close when done to release the mapping.
//
// A Store is safe for concurrent use: each View returns an independent
// Document aliasing the same immutable mapping.
type Store struct {
	image []byte
	f     *os.File
	layout sealedLayout
}

// OpenStore memory-maps path (a sealed .jstore file) and verifies it. The
// mapping is read-only; the file must not be modified while the Store is open.
func OpenStore(path string) (*Store, error) {
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
	l, ok := parseSealed(img)
	if !ok {
		munmap(img)
		f.Close()
		return nil, ErrBadIndex
	}
	if err := VerifyIndex(l.idx, l.data); err != nil {
		munmap(img)
		f.Close()
		return nil, err
	}
	return &Store{image: img, f: f, layout: l}, nil
}

// View returns an O(1) Document over the store's payload (no re-scan, no
// per-open hash — the mapping was verified at OpenStore). The Document is
// valid until Close.
func (s *Store) View() (*Document, error) {
	return ViewTrusted(s.layout.idx, s.layout.data)
}

// Data returns the raw payload bytes (aliasing the mapping).
func (s *Store) Data() []byte { return s.layout.data }

// Close releases the memory mapping and closes the file. Documents obtained
// from View must not be used after Close.
func (s *Store) Close() error {
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

// Set returns a new sealed store image with the value at keys replaced by
// setValue, using an incremental index patch (no full re-scan of the payload).
// The receiver is unchanged (stores are immutable); write the returned image to
// a new file (or atomically replace the old one) and re-open it. setValue must
// be valid JSON.
func (s *Store) Set(setValue []byte, keys ...string) ([]byte, error) {
	d, err := s.View()
	if err != nil {
		return nil, err
	}
	out, _, err := PatchSet(d, s.layout.data, setValue, keys...)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := Seal(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Delete returns a new sealed store image with the value at keys removed.
// If the path is absent the image is returned unchanged. The receiver is
// unchanged.
func (s *Store) Delete(keys ...string) ([]byte, error) {
	d, err := s.View()
	if err != nil {
		return nil, err
	}
	out, _, _, err := PatchDelete(d, s.layout.data, keys...)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := Seal(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}