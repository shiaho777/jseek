package jseek

import (
	"errors"
	"testing"
)

var sample = []byte(`{
  "person": {
    "name": { "first": "Leonid", "last": "Bugaev", "fullName": "Leonid Bugaev" },
    "github": { "handle": "buger", "followers": 109 },
    "avatars": [
      { "url": "https://avatars1.githubusercontent.com/u/14009?v=3&s=460", "type": "thumbnail" }
    ],
    "active": true,
    "score": 98.6,
    "nickname": null
  },
  "company": { "name": "Acme", "size": -42 },
  "tags": ["a", "b", "c"],
  "escaped": "line1\nline2\t\"quoted\"\u0041",
  "unicode": "\u00e9\u4e2d\ud83d\ude00"
}`)

func TestGetString(t *testing.T) {
	got, err := GetString(sample, "person", "name", "fullName")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "Leonid Bugaev" {
		t.Fatalf("got %q", got)
	}
}

func TestGetStringEscaped(t *testing.T) {
	got, err := GetString(sample, "escaped")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "line1\nline2\t\"quoted\"A"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestGetStringUnicode(t *testing.T) {
	got, err := GetString(sample, "unicode")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "é中😀"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestGetInt(t *testing.T) {
	got, err := GetInt(sample, "person", "github", "followers")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 109 {
		t.Fatalf("got %d", got)
	}
}

func TestGetIntNegative(t *testing.T) {
	got, err := GetInt(sample, "company", "size")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != -42 {
		t.Fatalf("got %d", got)
	}
}

func TestGetIntRejectsFloat(t *testing.T) {
	_, err := GetInt(sample, "person", "score")
	if !errors.Is(err, ErrUnexpectedType) {
		t.Fatalf("expected ErrUnexpectedType, got %v", err)
	}
}

func TestGetFloat(t *testing.T) {
	got, err := GetFloat(sample, "person", "score")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 98.6 {
		t.Fatalf("got %v", got)
	}
}

func TestGetBoolean(t *testing.T) {
	got, err := GetBoolean(sample, "person", "active")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Fatalf("expected true")
	}
}

func TestArrayIndex(t *testing.T) {
	got, err := GetString(sample, "person", "avatars", "[0]", "type")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "thumbnail" {
		t.Fatalf("got %q", got)
	}
}

func TestArrayIndexScalar(t *testing.T) {
	got, err := GetString(sample, "tags", "[2]")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "c" {
		t.Fatalf("got %q", got)
	}
}

func TestGetObject(t *testing.T) {
	v, vt, _, err := Get(sample, "company")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vt != Object {
		t.Fatalf("got type %v", vt)
	}
	// Nested lookup against the returned object slice should work too.
	name, err := GetString(v, "name")
	if err != nil || name != "Acme" {
		t.Fatalf("nested get failed: %q %v", name, err)
	}
}

func TestNull(t *testing.T) {
	_, vt, _, err := Get(sample, "person", "nickname")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vt != Null {
		t.Fatalf("got type %v", vt)
	}
}

func TestNotFound(t *testing.T) {
	_, _, _, err := Get(sample, "person", "missing")
	if !errors.Is(err, ErrKeyPathNotFound) {
		t.Fatalf("expected ErrKeyPathNotFound, got %v", err)
	}
}

func TestExists(t *testing.T) {
	if !Exists(sample, "person", "github", "handle") {
		t.Fatal("expected handle to exist")
	}
	if Exists(sample, "person", "nope") {
		t.Fatal("did not expect nope to exist")
	}
}

func TestArrayEach(t *testing.T) {
	var vals []string
	err := ArrayEach(sample, func(value []byte, dt ValueType, off int) bool {
		vals = append(vals, string(value))
		return true
	}, "tags")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vals) != 3 || vals[0] != "a" || vals[2] != "c" {
		t.Fatalf("got %v", vals)
	}
}

func TestArrayEachEarlyStop(t *testing.T) {
	count := 0
	err := ArrayEach(sample, func(value []byte, dt ValueType, off int) bool {
		count++
		return false
	}, "tags")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected early stop after 1, got %d", count)
	}
}

func TestObjectEach(t *testing.T) {
	keys := map[string]ValueType{}
	err := ObjectEach(sample, func(key, value []byte, dt ValueType, off int) bool {
		keys[string(key)] = dt
		return true
	}, "person", "github")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if keys["handle"] != String || keys["followers"] != Number {
		t.Fatalf("got %v", keys)
	}
}

func TestGetStringUnsafe(t *testing.T) {
	got, err := GetStringUnsafe(sample, "company", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "Acme" {
		t.Fatalf("got %q", got)
	}
}

func TestTopLevelValue(t *testing.T) {
	// With no keys, Get returns the whole document as an Object.
	_, vt, _, err := Get(sample)
	if err != nil || vt != Object {
		t.Fatalf("got type %v err %v", vt, err)
	}
}
