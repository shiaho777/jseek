package jseek

// Formatting: Compact and Indent re-serialize JSON without decoding it into
// Go values. They walk the raw bytes with the same SWAR-backed string scanner
// the extractor uses, so string bodies are copied in bulk runs rather than
// byte-by-byte. Both validate structure as they go and never mutate the input.
//
// These are the primitives a CLI or a gateway needs to normalize payloads:
// compact for storage/wire, indent for humans.

// Compact removes insignificant whitespace from JSON, returning a minified
// copy. It returns ErrMalformedJSON if the input is not a single well-formed
// JSON value (syntax-level, same contract as Valid). The input is never
// mutated. One allocation for the result (plus growth), zero otherwise.
func Compact(data []byte) ([]byte, error) {
	out := make([]byte, 0, len(data))
	i := skipWhitespace(data, 0)
	i, out, ok := compactValue(data, i, out)
	if !ok {
		return nil, ErrMalformedJSON
	}
	i = skipWhitespace(data, i)
	if i != len(data) {
		return nil, ErrMalformedJSON
	}
	return out, nil
}

// Indent pretty-prints JSON with the given indent string per nesting level
// (e.g. "  " or "\t"), returning a new slice. Objects and arrays are expanded
// one member/element per line; empty containers stay inline ({} / []).
// It returns ErrMalformedJSON on malformed input and never mutates the input.
func Indent(data []byte, indent string) ([]byte, error) {
	out := make([]byte, 0, len(data)+len(data)/4)
	i := skipWhitespace(data, 0)
	i, out, ok := indentValue(data, i, indent, 0, out)
	if !ok {
		return nil, ErrMalformedJSON
	}
	// encoding/json.Indent preserves trailing whitespace verbatim; match it.
	// (Leading whitespace is consumed; only the tail after the value stays.)
	end := skipWhitespace(data, i)
	if end != len(data) {
		return nil, ErrMalformedJSON
	}
	out = append(out, data[i:end]...)
	return out, nil
}

// compactValue copies one value from data[i] into out without whitespace.
func compactValue(data []byte, i int, out []byte) (int, []byte, bool) {
	if i >= len(data) {
		return i, out, false
	}
	switch data[i] {
	case '"':
		return compactString(data, i, out)
	case '{':
		return compactObject(data, i, out)
	case '[':
		return compactArray(data, i, out)
	case 't', 'f', 'n', '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		// Scalars: copy the verbatim token (permissive extent, like Get).
		_, ve, vt, ok := valueBounds(data, i)
		if !ok || vt == NotExist || vt == Unknown {
			return i, out, false
		}
		return ve, append(out, data[i:ve]...), true
	default:
		return i, out, false
	}
}

// compactString copies a string token including quotes, scanning the body in
// SWAR runs. Escapes are copied verbatim (no re-encoding).
func compactString(data []byte, i int, out []byte) (int, []byte, bool) {
	end, ok := skipString(data, i)
	if !ok {
		return end, out, false
	}
	return end, append(out, data[i:end]...), true
}

func compactObject(data []byte, i int, out []byte) (int, []byte, bool) {
	n := len(data)
	out = append(out, '{')
	i = skipWhitespace(data, i+1)
	if i < n && data[i] == '}' {
		return i + 1, append(out, '}'), true
	}
	for {
		if i >= n || data[i] != '"' {
			return i, out, false
		}
		var ok bool
		i, out, ok = compactString(data, i, out)
		if !ok {
			return i, out, false
		}
		i = skipWhitespace(data, i)
		if i >= n || data[i] != ':' {
			return i, out, false
		}
		out = append(out, ':')
		i = skipWhitespace(data, i+1)
		i, out, ok = compactValue(data, i, out)
		if !ok {
			return i, out, false
		}
		i = skipWhitespace(data, i)
		if i >= n {
			return i, out, false
		}
		switch data[i] {
		case ',':
			out = append(out, ',')
			i = skipWhitespace(data, i+1)
		case '}':
			return i + 1, append(out, '}'), true
		default:
			return i, out, false
		}
	}
}

func compactArray(data []byte, i int, out []byte) (int, []byte, bool) {
	n := len(data)
	out = append(out, '[')
	i = skipWhitespace(data, i+1)
	if i < n && data[i] == ']' {
		return i + 1, append(out, ']'), true
	}
	for {
		var ok bool
		i, out, ok = compactValue(data, i, out)
		if !ok {
			return i, out, false
		}
		i = skipWhitespace(data, i)
		if i >= n {
			return i, out, false
		}
		switch data[i] {
		case ',':
			out = append(out, ',')
			i = skipWhitespace(data, i+1)
		case ']':
			return i + 1, append(out, ']'), true
		default:
			return i, out, false
		}
	}
}

// indentValue pretty-prints one value at the given depth.
func indentValue(data []byte, i int, indent string, depth int, out []byte) (int, []byte, bool) {
	if i >= len(data) {
		return i, out, false
	}
	switch data[i] {
	case '"':
		return compactString(data, i, out)
	case '{':
		return indentObject(data, i, indent, depth, out)
	case '[':
		return indentArray(data, i, indent, depth, out)
	case 't', 'f', 'n', '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		_, ve, vt, ok := valueBounds(data, i)
		if !ok || vt == NotExist || vt == Unknown {
			return i, out, false
		}
		return ve, append(out, data[i:ve]...), true
	default:
		return i, out, false
	}
}

func writeIndent(out []byte, indent string, depth int) []byte {
	for k := 0; k < depth; k++ {
		out = append(out, indent...)
	}
	return out
}

func indentObject(data []byte, i int, indent string, depth int, out []byte) (int, []byte, bool) {
	n := len(data)
	i = skipWhitespace(data, i+1)
	if i < n && data[i] == '}' {
		return i + 1, append(out, '{', '}'), true
	}
	out = append(out, '{', '\n')
	for {
		if i >= n || data[i] != '"' {
			return i, out, false
		}
		out = writeIndent(out, indent, depth+1)
		var ok bool
		i, out, ok = compactString(data, i, out)
		if !ok {
			return i, out, false
		}
		i = skipWhitespace(data, i)
		if i >= n || data[i] != ':' {
			return i, out, false
		}
		out = append(out, ':', ' ')
		i = skipWhitespace(data, i+1)
		i, out, ok = indentValue(data, i, indent, depth+1, out)
		if !ok {
			return i, out, false
		}
		i = skipWhitespace(data, i)
		if i >= n {
			return i, out, false
		}
		switch data[i] {
		case ',':
			out = append(out, ',', '\n')
			i = skipWhitespace(data, i+1)
		case '}':
			out = append(out, '\n')
			out = writeIndent(out, indent, depth)
			return i + 1, append(out, '}'), true
		default:
			return i, out, false
		}
	}
}

func indentArray(data []byte, i int, indent string, depth int, out []byte) (int, []byte, bool) {
	n := len(data)
	i = skipWhitespace(data, i+1)
	if i < n && data[i] == ']' {
		return i + 1, append(out, '[', ']'), true
	}
	out = append(out, '[', '\n')
	for {
		out = writeIndent(out, indent, depth+1)
		var ok bool
		i, out, ok = indentValue(data, i, indent, depth+1, out)
		if !ok {
			return i, out, false
		}
		i = skipWhitespace(data, i)
		if i >= n {
			return i, out, false
		}
		switch data[i] {
		case ',':
			out = append(out, ',', '\n')
			i = skipWhitespace(data, i+1)
		case ']':
			out = append(out, '\n')
			out = writeIndent(out, indent, depth)
			return i + 1, append(out, ']'), true
		default:
			return i, out, false
		}
	}
}