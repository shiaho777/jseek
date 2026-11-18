// Package gjsoncompat is a drop-in replacement for github.com/tidwall/gjson
// backed by the jseek lazy-extraction engine. Existing gjson code can switch
// engines by changing one import line:
//
//   - import "github.com/tidwall/gjson"
//   - import gjson "github.com/shiaho777/jseek/gjsoncompat"
//
// The common API surface (Get, GetBytes, Parse, GetMany, GetManyBytes, Valid,
// Result with its typed accessors, and the Type constants) is preserved, so
// most call sites compile unchanged. Semantics follow gjson where gjson defines
// them; jseek's faster lazy walker does the work underneath.
//
// Compatibility notes:
//   - Result.Type values match gjson's (Null=0, False=1, Number=2, String=3,
//     True=4, JSON=5).
//   - Modifiers (@reverse, @pretty, etc.) and advanced gjson query features
//     beyond jseek's syntax are not implemented; such paths return a not-found
//     Result rather than panicking.
package gjsoncompat

import (
	"math"

	"github.com/shiaho777/jseek"
)

// Type is the type of a JSON value, matching gjson's numbering.
type Type int

const (
	Null   Type = 0
	False  Type = 1
	Number Type = 2
	String Type = 3
	True   Type = 4
	JSON   Type = 5
)

// Result represents a JSON value, mirroring gjson.Result's public fields and
// accessors.
type Result struct {
	Type Type
	Raw  string
	Str  string
	Num  float64
	// Index is the byte offset of the value in the original document where
	// known, else 0.
	Index int
}

// Exists reports whether the result was found.
func (r Result) Exists() bool { return r.Type != Null || r.Raw != "" }

// Value returns the value as an interface{}, like gjson.
func (r Result) Value() any {
	switch r.Type {
	case String:
		return r.Str
	case Number:
		return r.Num
	case True:
		return true
	case False:
		return false
	case JSON:
		return r.Raw
	default:
		return nil
	}
}

// String returns a string representation, like gjson.
func (r Result) String() string {
	switch r.Type {
	case String:
		return r.Str
	case Number, True, False, JSON:
		return r.Raw
	default:
		return ""
	}
}

// Int returns an int64, like gjson.
func (r Result) Int() int64 { return int64(r.Num) }

// Uint returns a uint64, like gjson.
func (r Result) Uint() uint64 { return uint64(r.Num) }

// Float returns a float64, like gjson.
func (r Result) Float() float64 { return r.Num }

// Bool returns a bool, like gjson.
func (r Result) Bool() bool { return r.Type == True }

// Array returns the elements of a JSON array result, like gjson.
func (r Result) Array() []Result {
	if r.Type != JSON || len(r.Raw) == 0 || r.Raw[0] != '[' {
		if r.Exists() {
			return []Result{r}
		}
		return nil
	}
	var out []Result
	jseek.ArrayEach([]byte(r.Raw), func(v []byte, vt jseek.ValueType, off int) bool {
		out = append(out, convert(v, vt))
		return true
	})
	return out
}

// IsArray reports whether the result is a JSON array.
func (r Result) IsArray() bool {
	return r.Type == JSON && len(r.Raw) > 0 && r.Raw[0] == '['
}

// IsObject reports whether the result is a JSON object.
func (r Result) IsObject() bool {
	return r.Type == JSON && len(r.Raw) > 0 && r.Raw[0] == '{'
}

// Map returns the members of a JSON object result, like gjson.
func (r Result) Map() map[string]Result {
	out := make(map[string]Result)
	if !r.IsObject() {
		return out
	}
	jseek.ObjectEach([]byte(r.Raw), func(k, v []byte, vt jseek.ValueType, off int) bool {
		key, _ := jseek.GetString(append(append([]byte{'"'}, k...), '"'))
		out[key] = convert(v, vt)
		return true
	})
	return out
}

// Get searches a nested JSON document for a path, like gjson.Get.
func Get(json, path string) Result {
	return GetBytes([]byte(json), path)
}

// GetBytes is Get over a byte slice, like gjson.GetBytes.
func GetBytes(data []byte, path string) Result {
	if path == "" {
		return ParseBytes(data)
	}
	// gjson returns a Number for a terminal "#" count (e.g. "users.#").
	if n, ok := countPath(data, path); ok {
		return Result{Type: Number, Raw: itoa(n), Num: float64(n)}
	}
	res, err := jseek.QueryTyped(data, path)
	if err != nil || len(res) == 0 {
		// gjson resolves a bare numeric segment against an object as a key
		// (jseek's dotted parser treats it as an array index). For a plain
		// single-segment path with no query syntax, fall back to the native
		// segment lookup, which matches gjson's object-key behavior.
		if isPlainKey(path) {
			if v, vt, _, e := jseek.Get(data, path); e == nil {
				return convert(v, vt)
			}
		}
		return Result{}
	}
	// gjson.Get returns the first match for a single-value path; for wildcard
	// paths it returns a JSON array of matches. Mirror that.
	if len(res) == 1 && !isMultiPath(path) {
		return convert(res[0].Raw, res[0].Type)
	}
	// Assemble a JSON array of the raw matches.
	raw := make([]byte, 0, 64)
	raw = append(raw, '[')
	for i, r := range res {
		if i > 0 {
			raw = append(raw, ',')
		}
		raw = append(raw, rawToken(r)...)
	}
	raw = append(raw, ']')
	return Result{Type: JSON, Raw: string(raw)}
}

// isPlainKey reports whether path is a single segment with no query syntax.
func isPlainKey(path string) bool {
	for i := 0; i < len(path); i++ {
		switch path[i] {
		case '.', '#', '*', '[', ']', '|', '@', '\\':
			return false
		}
	}
	return len(path) > 0
}

// countPath handles a terminal "#" (or ".#") count, returning the cardinality
// of the container at the prefix path. ok is false if the path is not a count.
func countPath(data []byte, path string) (int, bool) {
	if path == "#" {
		n, err := jseek.Count(data)
		return n, err == nil
	}
	if len(path) >= 2 && path[len(path)-1] == '#' && path[len(path)-2] == '.' {
		prefix := path[:len(path)-2]
		keys := jseek.ParseDotPath(prefix)
		n, err := jseek.Count(data, keys...)
		return n, err == nil
	}
	return 0, false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// isMultiPath reports whether a path uses jseek/gjson multi-match syntax.
func isMultiPath(path string) bool {
	for i := 0; i < len(path); i++ {
		switch path[i] {
		case '#', '*':
			return true
		}
		if path[i] == '.' && i+1 < len(path) && path[i+1] == '.' {
			return true
		}
	}
	return false
}

// Parse parses a whole JSON document into a Result, like gjson.Parse.
func Parse(json string) Result { return ParseBytes([]byte(json)) }

// ParseBytes is Parse over a byte slice.
func ParseBytes(data []byte) Result {
	v, vt, _, err := jseek.Get(data)
	if err != nil {
		return Result{}
	}
	return convert(v, vt)
}

// GetMany returns multiple paths at once, like gjson.GetMany.
func GetMany(json string, paths ...string) []Result {
	return GetManyBytes([]byte(json), paths...)
}

// GetManyBytes is GetMany over a byte slice.
func GetManyBytes(data []byte, paths ...string) []Result {
	out := make([]Result, len(paths))
	for i, p := range paths {
		out[i] = GetBytes(data, p)
	}
	return out
}

// Valid reports whether the input is valid JSON, like gjson.Valid.
func Valid(json string) bool { return jseek.Valid([]byte(json)) }

// ValidBytes is Valid over a byte slice.
func ValidBytes(data []byte) bool { return jseek.Valid(data) }

// convert maps a jseek typed value to a gjsoncompat Result.
func convert(raw []byte, vt jseek.ValueType) Result {
	switch vt {
	case jseek.String:
		s, _ := jseek.GetString(append(append([]byte{'"'}, raw...), '"'))
		return Result{Type: String, Raw: string(append(append([]byte{'"'}, raw...), '"')), Str: s}
	case jseek.Number:
		f, err := jseek.GetFloat(raw)
		if err != nil {
			// gjson's lenient parser yields ±Inf on overflow; match it.
			f = math.Inf(1)
			if len(raw) > 0 && raw[0] == '-' {
				f = math.Inf(-1)
			}
		}
		return Result{Type: Number, Raw: string(raw), Num: f}
	case jseek.Boolean:
		if string(raw) == "true" {
			return Result{Type: True, Raw: "true"}
		}
		return Result{Type: False, Raw: "false"}
	case jseek.Null:
		return Result{Type: Null, Raw: "null"}
	case jseek.Object, jseek.Array:
		return Result{Type: JSON, Raw: string(raw)}
	default:
		return Result{}
	}
}

// rawToken renders a typed result as a standalone JSON token.
func rawToken(r jseek.QueryResult) []byte {
	if r.Type == jseek.String {
		return append(append([]byte{'"'}, r.Raw...), '"')
	}
	return r.Raw
}
