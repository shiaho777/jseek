package jseek

import (
	"bufio"
	"errors"
	"io"
)

// Streaming decode for inputs too large to hold in memory at once.
//
// The dominant shape of "huge JSON" in practice is a long sequence of records:
// either a top-level array of objects ([{...},{...},...]) or newline-delimited
// JSON (NDJSON / JSON Lines). A Decoder reads from an io.Reader and yields one
// complete top-level element at a time, so memory stays bounded by the largest
// single element rather than the whole stream. Each yielded element is a
// self-contained value slice on which the full jseek API (Get, Index, EachKey,
// ArrayEach, ...) can be used.

// ErrTooLarge is returned when a single element exceeds the Decoder's MaxValue
// limit, which guards against unbounded buffering on malformed or hostile
// input.
var ErrTooLarge = errors.New("jseek: value exceeds maximum size")

// Decoder reads a stream of JSON values from an io.Reader. It auto-detects a
// top-level array (yielding each element) versus a sequence of whitespace- or
// newline-separated values (yielding each value). It is not safe for concurrent
// use.
type Decoder struct {
	r   *bufio.Reader
	buf []byte // holds the current element bytes (reused across calls)

	// MaxValue caps the size in bytes of a single element. Zero means the
	// default (64 MiB). Set before the first Next/ForEach call.
	MaxValue int

	started bool // have we consumed the optional leading '['?
	inArray bool // are we inside a top-level array?
	done    bool // stream exhausted
	err     error
}

const defaultMaxValue = 64 << 20 // 64 MiB

// NewDecoder returns a Decoder reading from r.
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{r: bufio.NewReaderSize(r, 64<<10)}
}

func (d *Decoder) maxValue() int {
	if d.MaxValue > 0 {
		return d.MaxValue
	}
	return defaultMaxValue
}

// readByteSkipSpace returns the next non-whitespace byte, or an error.
func (d *Decoder) readByteSkipSpace() (byte, error) {
	for {
		c, err := d.r.ReadByte()
		if err != nil {
			return 0, err
		}
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			continue
		}
		return c, nil
	}
}

// Next reads and returns the next top-level element of the stream. The returned
// slice is valid only until the following call to Next (the buffer is reused);
// copy it if you need to retain it. When the stream is exhausted Next returns
// (nil, io.EOF).
func (d *Decoder) Next() ([]byte, error) {
	if d.err != nil {
		return nil, d.err
	}
	if d.done {
		return nil, io.EOF
	}

	if !d.started {
		d.started = true
		c, err := d.readByteSkipSpace()
		if err != nil {
			if err == io.EOF {
				d.done = true
			}
			d.err = err
			return nil, err
		}
		if c == '[' {
			d.inArray = true
		} else {
			// Not an array: this byte is the first byte of the first value.
			if uerr := d.r.UnreadByte(); uerr != nil {
				d.err = uerr
				return nil, uerr
			}
		}
	}

	if d.inArray {
		// Peek for end-of-array or a separating comma.
		c, err := d.readByteSkipSpace()
		if err != nil {
			d.err = err
			return nil, err
		}
		switch c {
		case ']':
			d.done = true
			return nil, io.EOF
		case ',':
			// Another element follows; fall through to read it.
		default:
			// First element: put the byte back and read the value.
			if uerr := d.r.UnreadByte(); uerr != nil {
				d.err = uerr
				return nil, uerr
			}
		}
	}

	// Determine the start of the next value.
	c, err := d.readByteSkipSpace()
	if err != nil {
		if err == io.EOF {
			d.done = true
		}
		d.err = err
		return nil, err
	}
	if err := d.r.UnreadByte(); err != nil {
		d.err = err
		return nil, err
	}
	if d.inArray && c == ']' {
		// Trailing ] after a comma is malformed, but tolerate by ending.
		_, _ = d.r.ReadByte()
		d.done = true
		return nil, io.EOF
	}

	return d.readValue()
}

// readValue reads exactly one complete JSON value from the reader into d.buf,
// tracking string state and brace/bracket depth so it stops at the value's end.
func (d *Decoder) readValue() ([]byte, error) {
	d.buf = d.buf[:0]
	max := d.maxValue()

	first, err := d.r.ReadByte()
	if err != nil {
		d.err = err
		return nil, err
	}
	d.buf = append(d.buf, first)

	switch first {
	case '{', '[':
		return d.readContainer()
	case '"':
		return d.readString()
	default:
		// Scalar: read until a structural boundary (whitespace, , ] } or EOF).
		for {
			c, err := d.r.ReadByte()
			if err != nil {
				if err == io.EOF {
					return d.buf, nil
				}
				d.err = err
				return nil, err
			}
			if c == ' ' || c == '\t' || c == '\n' || c == '\r' ||
				c == ',' || c == ']' || c == '}' {
				_ = d.r.UnreadByte()
				return d.buf, nil
			}
			d.buf = append(d.buf, c)
			if len(d.buf) > max {
				d.err = ErrTooLarge
				return nil, ErrTooLarge
			}
		}
	}
}

// readContainer reads the remainder of an object/array whose opening byte is
// already in d.buf. It scans the bufio buffer directly (Peek/Discard) instead
// of one ReadByte per byte, and uses the SWAR scanner to skip string bodies in
// bulk — eliminating the per-byte call overhead that dominates byte-stream
// decoding.
func (d *Decoder) readContainer() ([]byte, error) {
	max := d.maxValue()
	depth := 1
	inStr := false
	esc := false
	for {
		chunk, perr := d.r.Peek(d.r.Buffered())
		if len(chunk) == 0 {
			// Buffer empty: force a refill by peeking one byte.
			chunk, perr = d.r.Peek(1)
			if len(chunk) == 0 {
				if perr == io.EOF || perr == bufio.ErrBufferFull {
					d.err = io.ErrUnexpectedEOF
					return nil, io.ErrUnexpectedEOF
				}
				d.err = perr
				return nil, perr
			}
		}
		k := 0
		for k < len(chunk) {
			if inStr {
				// Jump to the next quote or backslash within the chunk.
				j := indexQuoteOrBackslash(chunk, k)
				if j < 0 {
					k = len(chunk)
					break
				}
				if chunk[j] == '\\' {
					if j+1 < len(chunk) {
						k = j + 2 // skip the escaped byte
						continue
					}
					// Escape at chunk boundary: consume through '\' and set a
					// pending-escape so the next chunk's first byte is skipped.
					k = j + 1
					esc = true
					break
				}
				inStr = false
				k = j + 1
				continue
			}
			if esc {
				esc = false
				k++
				continue
			}
			c := chunk[k]
			switch c {
			case '"':
				inStr = true
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					k++
					d.buf = append(d.buf, chunk[:k]...)
					if len(d.buf) > max {
						d.err = ErrTooLarge
						return nil, ErrTooLarge
					}
					if _, err := d.r.Discard(k); err != nil {
						d.err = err
						return nil, err
					}
					return d.buf, nil
				}
			}
			k++
		}
		d.buf = append(d.buf, chunk[:k]...)
		if len(d.buf) > max {
			d.err = ErrTooLarge
			return nil, ErrTooLarge
		}
		if _, err := d.r.Discard(k); err != nil {
			d.err = err
			return nil, err
		}
	}
}

// readString reads the remainder of a string whose opening quote is already in
// d.buf, scanning the buffer directly with the SWAR quote/backslash finder.
func (d *Decoder) readString() ([]byte, error) {
	max := d.maxValue()
	esc := false
	for {
		chunk, perr := d.r.Peek(d.r.Buffered())
		if len(chunk) == 0 {
			chunk, perr = d.r.Peek(1)
			if len(chunk) == 0 {
				if perr == io.EOF || perr == bufio.ErrBufferFull {
					d.err = io.ErrUnexpectedEOF
					return nil, io.ErrUnexpectedEOF
				}
				d.err = perr
				return nil, perr
			}
		}
		k := 0
		for k < len(chunk) {
			if esc {
				esc = false
				k++
				continue
			}
			j := indexQuoteOrBackslash(chunk, k)
			if j < 0 {
				k = len(chunk)
				break
			}
			if chunk[j] == '\\' {
				if j+1 < len(chunk) {
					k = j + 2
					continue
				}
				k = j + 1
				esc = true
				break
			}
			// Closing quote.
			k = j + 1
			d.buf = append(d.buf, chunk[:k]...)
			if len(d.buf) > max {
				d.err = ErrTooLarge
				return nil, ErrTooLarge
			}
			if _, err := d.r.Discard(k); err != nil {
				d.err = err
				return nil, err
			}
			return d.buf, nil
		}
		d.buf = append(d.buf, chunk[:k]...)
		if len(d.buf) > max {
			d.err = ErrTooLarge
			return nil, ErrTooLarge
		}
		if _, err := d.r.Discard(k); err != nil {
			d.err = err
			return nil, err
		}
	}
}

// ForEach reads the entire stream, invoking cb for each top-level element. It
// stops and returns the callback's error if cb returns a non-nil error, or the
// first decode error encountered. A clean end of stream returns nil.
//
// The element slice passed to cb is only valid for the duration of the call.
func (d *Decoder) ForEach(cb func(value []byte) error) error {
	for {
		v, err := d.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if cberr := cb(v); cberr != nil {
			return cberr
		}
	}
}
