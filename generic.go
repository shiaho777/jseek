package jseek

// Generic, type-safe accessors built on the core getters. These give jseek a
// modern API surface: At[int64](data, "a", "b") instead of remembering a
// separate method name per type, with a compile-time type guarantee.

// Scalar is the set of Go types At and Value can extract directly from JSON
// scalars.
type Scalar interface {
	~string | ~bool | ~int64 | ~float64
}

// At extracts the value at the key path and returns it as type T, where T is
// one of string, bool, int64, or float64. It returns the zero value and an
// error if the path is missing or the JSON value is not convertible to T.
//
//	name, err := jseek.At[string](data, "user", "name")
//	n, err := jseek.At[int64](data, "user", "followers")
func At[T Scalar](data []byte, keys ...string) (T, error) {
	var zero T
	switch any(zero).(type) {
	case string:
		s, err := GetString(data, keys...)
		if err != nil {
			return zero, enrich(err, data, keys, String)
		}
		return any(s).(T), nil
	case bool:
		b, err := GetBoolean(data, keys...)
		if err != nil {
			return zero, enrich(err, data, keys, Boolean)
		}
		return any(b).(T), nil
	case int64:
		n, err := GetInt(data, keys...)
		if err != nil {
			return zero, enrich(err, data, keys, Number)
		}
		return any(n).(T), nil
	case float64:
		f, err := GetFloat(data, keys...)
		if err != nil {
			return zero, enrich(err, data, keys, Number)
		}
		return any(f).(T), nil
	default:
		return zero, ErrUnexpectedType
	}
}

// enrich wraps a bare sentinel error from the fast getters with diagnosable
// path/type context. The actual value type is recovered cheaply via a single
// Get when the failure is a type mismatch.
func enrich(err error, data []byte, keys []string, want ValueType) error {
	switch err {
	case ErrKeyPathNotFound:
		return newNotFound(data, keys)
	case ErrUnexpectedType:
		got := Unknown
		if _, vt, _, gerr := Get(data, keys...); gerr == nil {
			got = vt
		}
		return &PathError{Err: ErrUnexpectedType, Path: keys, At: -1, Offset: -1, Got: got, Want: want}
	case ErrOverflow:
		return &PathError{Err: ErrOverflow, Path: keys, At: -1, Offset: -1, Got: Number, Want: want}
	default:
		return err
	}
}

// Or extracts the value at the key path as type T, returning fallback if the
// path is missing or the type does not match. It never returns an error, which
// makes it convenient for optional fields with defaults.
//
//	port := jseek.Or[int64](cfg, 8080, "server", "port")
func Or[T Scalar](data []byte, fallback T, keys ...string) T {
	v, err := At[T](data, keys...)
	if err != nil {
		return fallback
	}
	return v
}
