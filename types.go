package jseek

import "errors"

// ValueType identifies the JSON type of an extracted value.
type ValueType int

const (
	// NotExist means the requested path was not found in the document.
	NotExist ValueType = iota
	// String is a JSON string (the returned bytes exclude the surrounding quotes).
	String
	// Number is a JSON number (integer or floating point).
	Number
	// Object is a JSON object, returned as the raw bytes including braces.
	Object
	// Array is a JSON array, returned as the raw bytes including brackets.
	Array
	// Boolean is a JSON true or false.
	Boolean
	// Null is a JSON null.
	Null
	// Unknown is reserved for malformed or unrecognized input.
	Unknown
)

// String returns a human-readable name for the value type.
func (t ValueType) String() string {
	switch t {
	case NotExist:
		return "non-existent"
	case String:
		return "string"
	case Number:
		return "number"
	case Object:
		return "object"
	case Array:
		return "array"
	case Boolean:
		return "boolean"
	case Null:
		return "null"
	default:
		return "unknown"
	}
}

// Sentinel errors returned by the public API. Callers may compare against these
// with errors.Is.
var (
	// ErrKeyPathNotFound is returned when a requested key or index does not exist.
	ErrKeyPathNotFound = errors.New("jseek: key path not found")
	// ErrMalformedJSON is returned when the input is not valid JSON in the
	// region jseek needed to inspect.
	ErrMalformedJSON = errors.New("jseek: malformed JSON")
	// ErrUnexpectedType is returned by typed getters when the value at the path
	// is not of the requested type.
	ErrUnexpectedType = errors.New("jseek: value is not of the expected type")
	// ErrOverflow is returned when a number cannot be represented in the
	// requested Go type.
	ErrOverflow = errors.New("jseek: number overflows target type")
)
