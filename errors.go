package jseek

import (
	"strconv"
	"strings"
)

// PathError adds context to a sentinel error: which key path was being
// resolved, how far along the path resolution got, and (where known) the byte
// offset in the document. It wraps one of the package sentinel errors so that
// errors.Is(err, ErrKeyPathNotFound) and friends continue to work.
//
// The read-fast helpers (Get, GetInt, ...) return the bare sentinel errors for
// zero-overhead error handling. The richer GetX / MustGet helpers and the
// generic accessors attach a PathError so failures are diagnosable.
type PathError struct {
	// Err is the underlying sentinel (ErrKeyPathNotFound, ErrUnexpectedType, ...).
	Err error
	// Path is the full key path that was requested.
	Path []string
	// At is the index within Path of the segment that failed. -1 if not
	// applicable (e.g. a type error at the final value).
	At int
	// Offset is the byte offset in the document where resolution stopped, or
	// -1 if unknown.
	Offset int
	// Got is the value type actually found, when the error is a type mismatch.
	Got ValueType
	// Want is the value type that was expected, when applicable.
	Want ValueType
}

// Error implements error with a human-readable, diagnosable message.
func (e *PathError) Error() string {
	var b strings.Builder
	b.WriteString("jseek: ")
	switch e.Err {
	case ErrKeyPathNotFound:
		b.WriteString("key path not found")
		if e.At >= 0 && e.At < len(e.Path) {
			b.WriteString(": stopped at ")
			b.WriteString(strconv.Quote(e.Path[e.At]))
			b.WriteString(" in path ")
			b.WriteString(formatPath(e.Path))
		}
	case ErrUnexpectedType:
		b.WriteString("unexpected type")
		if e.Want != Unknown || e.Got != Unknown {
			b.WriteString(": wanted ")
			b.WriteString(e.Want.String())
			b.WriteString(", got ")
			b.WriteString(e.Got.String())
		}
		if len(e.Path) > 0 {
			b.WriteString(" at path ")
			b.WriteString(formatPath(e.Path))
		}
	case ErrMalformedJSON:
		b.WriteString("malformed JSON")
		if e.Offset >= 0 {
			b.WriteString(" at byte offset ")
			b.WriteString(strconv.Itoa(e.Offset))
		}
	case ErrOverflow:
		b.WriteString("number overflows target type")
		if len(e.Path) > 0 {
			b.WriteString(" at path ")
			b.WriteString(formatPath(e.Path))
		}
	default:
		b.WriteString(e.Err.Error())
	}
	return b.String()
}

// Unwrap returns the underlying sentinel error so errors.Is/As work.
func (e *PathError) Unwrap() error { return e.Err }

// formatPath renders a key path as a dotted expression for messages, e.g.
// ["a","[0]","b"] -> a[0].b
func formatPath(path []string) string {
	var b strings.Builder
	for i, seg := range path {
		if len(seg) > 0 && seg[0] == '[' {
			b.WriteString(seg)
			continue
		}
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(seg)
	}
	if b.Len() == 0 {
		return "(root)"
	}
	return b.String()
}

// newNotFound builds a PathError for a missing path, recording how far seek got.
func newNotFound(data []byte, path []string) *PathError {
	at, off := resolveFailurePoint(data, path)
	return &PathError{Err: ErrKeyPathNotFound, Path: path, At: at, Offset: off, Got: Unknown, Want: Unknown}
}

// resolveFailurePoint re-walks path to find the first segment that cannot be
// resolved, for diagnostic reporting. It is only called on the error path, so
// its cost does not affect successful lookups.
func resolveFailurePoint(data []byte, path []string) (failedAt, offset int) {
	i := skipWhitespace(data, 0)
	for idx, key := range path {
		if i >= len(data) {
			return idx, i
		}
		if n, isIdx := parseArrayIndex(key); isIdx {
			if data[i] != '[' {
				return idx, i
			}
			ni, ok := findIndex(data, i, n)
			if !ok {
				return idx, i
			}
			i = ni
		} else {
			if data[i] != '{' {
				return idx, i
			}
			ni, ok := findKey(data, i, key)
			if !ok {
				return idx, i
			}
			i = ni
		}
	}
	return -1, i
}
