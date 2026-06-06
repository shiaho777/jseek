package jseek

// Result is a lazily-typed view of a single extracted value. It holds the raw
// bytes and the value type, and offers typed accessors that decode on demand.
// It is returned by the multi-path GetMany family so callers can pull several
// fields and then interpret each one without separate Get calls.
//
// A Result's Raw bytes alias the source document; treat them as valid only
// while that document is unmodified and live.
type Result struct {
	// Raw is the value bytes (quotes stripped for strings; delimiters included
	// for objects/arrays). Nil when the path did not exist.
	Raw []byte
	// Type is the value's JSON type, or NotExist if the path was absent.
	Type ValueType
}

// Exists reports whether the result corresponds to a value that was found.
func (r Result) Exists() bool { return r.Type != NotExist }

// String decodes the value as a Go string. For String values, escapes are
// decoded. For non-string values, the raw token text is returned (e.g. a number
// as its digits), which is convenient for display.
func (r Result) String() string {
	if r.Type == String {
		return string(unescapeInto(nil, r.Raw))
	}
	return string(r.Raw)
}

// Int decodes the value as an int64. ok is false if the value is not an integer.
func (r Result) Int() (int64, bool) {
	if r.Type != Number {
		return 0, false
	}
	v, err := parseInt(r.Raw)
	return v, err == nil
}

// Float decodes the value as a float64. ok is false if the value is not a number.
func (r Result) Float() (float64, bool) {
	if r.Type != Number {
		return 0, false
	}
	v, err := parseFloat(r.Raw)
	return v, err == nil
}

// Bool decodes the value as a bool. ok is false if the value is not a boolean.
func (r Result) Bool() (bool, bool) {
	if r.Type != Boolean {
		return false, false
	}
	v, err := parseBool(r.Raw)
	return v, err == nil
}

// Bytes returns the raw value bytes (aliasing the document).
func (r Result) Bytes() []byte { return r.Raw }

// GetMany extracts multiple paths in a single document pass and returns a
// Result for each, in the same order as paths. Missing paths yield a Result
// with Type == NotExist. It is the typed, ordered counterpart to EachKey.
//
// For repeated queries with the same path set, compile once with CompileStrings
// and call Paths.GetMany to avoid recompiling.
func GetMany(data []byte, paths ...[]string) []Result {
	return CompileStrings(paths...).GetMany(data)
}

// GetMany runs the compiled path set against data and returns ordered Results.
func (p *Paths) GetMany(data []byte) []Result {
	// Determine how many input paths there were by scanning terminals.
	n := p.countPaths()
	results := make([]Result, n)
	p.Each(data, func(idx int, value []byte, vt ValueType, err error) {
		if err != nil || idx < 0 || idx >= n {
			return
		}
		results[idx] = Result{Raw: value, Type: vt}
	})
	return results
}

// countPaths returns the number of distinct input paths compiled into p.
func (p *Paths) countPaths() int {
	max := -1
	for i := range p.nodes {
		for _, t := range p.nodes[i].terminal {
			if t > max {
				max = t
			}
		}
	}
	return max + 1
}
