package jseek

import (
	"reflect"
	"testing"
)

func TestParseDotPath(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"a.b.c", []string{"a", "b", "c"}},
		{"a[0].b", []string{"a", "[0]", "b"}},
		{"users.0.name", []string{"users", "[0]", "name"}},
		{"a", []string{"a"}},
		{"", nil},
		{`a\.b.c`, []string{"a.b", "c"}},
		{"arr[10][2]", []string{"arr", "[10]", "[2]"}},
	}
	for _, c := range cases {
		got := ParseDotPath(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseDotPath(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParsePointer(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"/a/b/c", []string{"a", "b", "c"}},
		{"/a/0/b", []string{"a", "[0]", "b"}},
		{"", nil},
		{"/foo~1bar", []string{"foo/bar"}},
		{"/foo~0bar", []string{"foo~bar"}},
	}
	for _, c := range cases {
		got, err := ParsePointer(c.in)
		if err != nil {
			t.Errorf("ParsePointer(%q) error: %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParsePointer(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParsePointerInvalid(t *testing.T) {
	if _, err := ParsePointer("no-leading-slash"); err == nil {
		t.Fatal("expected error for pointer without leading slash")
	}
}

func TestGetPath(t *testing.T) {
	s, _, _, err := GetPath(sample, "person.name.fullName")
	if err != nil || string(s) != "Leonid Bugaev" {
		t.Fatalf("GetPath: %q %v", s, err)
	}
	s, _, _, err = GetPath(sample, "person.avatars[0].type")
	if err != nil || string(s) != "thumbnail" {
		t.Fatalf("GetPath indexed: %q %v", s, err)
	}
}

func TestGetPointer(t *testing.T) {
	s, _, _, err := GetPointer(sample, "/person/github/handle")
	if err != nil || string(s) != "buger" {
		t.Fatalf("GetPointer: %q %v", s, err)
	}
	s, _, _, err = GetPointer(sample, "/person/avatars/0/type")
	if err != nil || string(s) != "thumbnail" {
		t.Fatalf("GetPointer indexed: %q %v", s, err)
	}
}

func TestDocumentGetPathAndPointer(t *testing.T) {
	d := Index(sample)
	s, _, _, err := d.GetPath("company.name")
	if err != nil || string(s) != "Acme" {
		t.Fatalf("Document.GetPath: %q %v", s, err)
	}
	s, _, _, err = d.GetPointer("/company/name")
	if err != nil || string(s) != "Acme" {
		t.Fatalf("Document.GetPointer: %q %v", s, err)
	}
}
