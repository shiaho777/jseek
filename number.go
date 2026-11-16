package jseek

import (
	"math"
	"strconv"
	"unsafe"
)

// parseInt parses a JSON integer token in raw into an int64 without allocating.
// It rejects values containing a fraction or exponent so that callers asking
// for an integer do not silently truncate floats.
func parseInt(raw []byte) (int64, error) {
	if len(raw) == 0 {
		return 0, ErrMalformedJSON
	}
	i := 0
	neg := false
	if raw[0] == '-' {
		neg = true
		i++
		if i == len(raw) {
			return 0, ErrMalformedJSON
		}
	}
	// Digit-count bound instead of a per-digit division: any run of at most
	// 19 digits fits in uint64 (10^19-1 < 2^64), so only the 20th digit can
	// overflow, and only when the accumulated value exceeds MaxUint64/10
	// (or equals it with a final digit above MaxUint64%10). The int64 range
	// check below still applies afterwards.
	digits := 0
	var n uint64
	for ; i < len(raw); i++ {
		c := raw[i]
		if c < '0' || c > '9' {
			// A fraction or exponent means this is not an integer.
			return 0, ErrUnexpectedType
		}
		digits++
		if digits >= 20 && (n > math.MaxUint64/10 || (n == math.MaxUint64/10 && c-'0' > math.MaxUint64%10)) {
			return 0, ErrOverflow
		}
		n = n*10 + uint64(c-'0')
	}
	if neg {
		if n > uint64(math.MaxInt64)+1 {
			return 0, ErrOverflow
		}
		return -int64(n), nil
	}
	if n > uint64(math.MaxInt64) {
		return 0, ErrOverflow
	}
	return int64(n), nil
}

// parseFloat parses a JSON number token in raw into a float64. It uses the
// standard library parser on a no-copy string view of raw, which the stdlib
// fast path handles without allocation for typical numbers.
func parseFloat(raw []byte) (float64, error) {
	if len(raw) == 0 {
		return 0, ErrMalformedJSON
	}
	s := unsafe.String(unsafe.SliceData(raw), len(raw))
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, ErrMalformedJSON
	}
	return f, nil
}

// parseBool parses a JSON boolean literal token.
func parseBool(raw []byte) (bool, error) {
	switch {
	case len(raw) == 4 && raw[0] == 't' && raw[1] == 'r' && raw[2] == 'u' && raw[3] == 'e':
		return true, nil
	case len(raw) == 5 && raw[0] == 'f' && raw[1] == 'a' && raw[2] == 'l' && raw[3] == 's' && raw[4] == 'e':
		return false, nil
	default:
		return false, ErrUnexpectedType
	}
}
