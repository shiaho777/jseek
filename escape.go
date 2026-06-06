package jseek

import (
	"unicode/utf8"
)

// escapedEquals reports whether the raw JSON string content raw (the bytes
// between the quotes, containing escape sequences) decodes to exactly the plain
// Go string want. It performs no allocation.
func escapedEquals(raw []byte, want string) bool {
	wi := 0
	i := 0
	for i < len(raw) {
		if wi > len(want) {
			return false
		}
		c := raw[i]
		if c != '\\' {
			if wi >= len(want) || want[wi] != c {
				return false
			}
			i++
			wi++
			continue
		}
		// Escape sequence.
		if i+1 >= len(raw) {
			return false
		}
		switch raw[i+1] {
		case '"', '\\', '/':
			if wi >= len(want) || want[wi] != raw[i+1] {
				return false
			}
			wi++
			i += 2
		case 'b':
			if wi >= len(want) || want[wi] != '\b' {
				return false
			}
			wi++
			i += 2
		case 'f':
			if wi >= len(want) || want[wi] != '\f' {
				return false
			}
			wi++
			i += 2
		case 'n':
			if wi >= len(want) || want[wi] != '\n' {
				return false
			}
			wi++
			i += 2
		case 'r':
			if wi >= len(want) || want[wi] != '\r' {
				return false
			}
			wi++
			i += 2
		case 't':
			if wi >= len(want) || want[wi] != '\t' {
				return false
			}
			wi++
			i += 2
		case 'u':
			r, sz, ok := decodeUnicodeEscape(raw, i)
			if !ok {
				return false
			}
			var buf [4]byte
			rn := utf8.EncodeRune(buf[:], r)
			if wi+rn > len(want) {
				return false
			}
			for k := 0; k < rn; k++ {
				if want[wi+k] != buf[k] {
					return false
				}
			}
			wi += rn
			i += sz
		default:
			return false
		}
	}
	return wi == len(want)
}

// hexVal returns the value of a single hex digit, or -1 if invalid.
func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	default:
		return -1
	}
}

// decodeUnicodeEscape decodes a \uXXXX sequence (and a following surrogate-pair
// \uXXXX if present) starting at raw[i] where raw[i]=='\\' and raw[i+1]=='u'.
// It returns the decoded rune, the number of bytes consumed from i, and ok.
func decodeUnicodeEscape(raw []byte, i int) (rune, int, bool) {
	r, ok := readHex4(raw, i+2)
	if !ok {
		return utf8.RuneError, 0, false
	}
	r1 := rune(r)
	if r1 >= 0xD800 && r1 <= 0xDBFF {
		// High surrogate; expect a following low surrogate.
		if i+6+6 <= len(raw) && raw[i+6] == '\\' && raw[i+7] == 'u' {
			r2v, ok2 := readHex4(raw, i+8)
			if ok2 {
				r2 := rune(r2v)
				if r2 >= 0xDC00 && r2 <= 0xDFFF {
					combined := 0x10000 + (r1-0xD800)<<10 + (r2 - 0xDC00)
					return combined, 12, true
				}
			}
		}
		return utf8.RuneError, 6, true
	}
	if r1 >= 0xDC00 && r1 <= 0xDFFF {
		// Lone low surrogate.
		return utf8.RuneError, 6, true
	}
	return r1, 6, true
}

// readHex4 reads exactly four hex digits at raw[j:j+4].
func readHex4(raw []byte, j int) (uint32, bool) {
	if j+4 > len(raw) {
		return 0, false
	}
	var v uint32
	for k := 0; k < 4; k++ {
		h := hexVal(raw[j+k])
		if h < 0 {
			return 0, false
		}
		v = v<<4 | uint32(h)
	}
	return v, true
}

// unescapeInto decodes the raw JSON string content raw into dst, growing dst as
// needed, and returns the result. When raw contains no escapes the input is
// appended verbatim. dst may be nil.
func unescapeInto(dst, raw []byte) []byte {
	// Fast path: no escapes.
	hasEscape := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '\\' {
			hasEscape = true
			break
		}
	}
	if !hasEscape {
		return append(dst, raw...)
	}

	i := 0
	for i < len(raw) {
		c := raw[i]
		if c != '\\' {
			dst = append(dst, c)
			i++
			continue
		}
		if i+1 >= len(raw) {
			// Trailing backslash; emit verbatim.
			dst = append(dst, c)
			i++
			continue
		}
		switch raw[i+1] {
		case '"':
			dst = append(dst, '"')
			i += 2
		case '\\':
			dst = append(dst, '\\')
			i += 2
		case '/':
			dst = append(dst, '/')
			i += 2
		case 'b':
			dst = append(dst, '\b')
			i += 2
		case 'f':
			dst = append(dst, '\f')
			i += 2
		case 'n':
			dst = append(dst, '\n')
			i += 2
		case 'r':
			dst = append(dst, '\r')
			i += 2
		case 't':
			dst = append(dst, '\t')
			i += 2
		case 'u':
			r, sz, ok := decodeUnicodeEscape(raw, i)
			if !ok {
				dst = append(dst, c)
				i++
				continue
			}
			var buf [4]byte
			n := utf8.EncodeRune(buf[:], r)
			dst = append(dst, buf[:n]...)
			i += sz
		default:
			// Unknown escape; emit the backslash verbatim.
			dst = append(dst, c)
			i++
		}
	}
	return dst
}
