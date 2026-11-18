package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// buildCLI builds the jseek binary once for the test suite.
func buildCLI(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "jseek-test")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	return bin
}

func run(t *testing.T, bin string, input string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	if input != "" {
		cmd.Stdin = bytes.NewBufferString(input)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run failed: %v\n%s", err, errb.String())
	}
	return out.String(), code
}

func TestCLIQuery(t *testing.T) {
	bin := buildCLI(t)
	doc := `{"user":{"name":"Ada","age":36},"users":[{"name":"Bob","age":24},{"name":"Cleo","age":41}],"note":"42"}`
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"user.name"}, "\"Ada\"\n"},
		{[]string{"-r", "user.name"}, "Ada\n"},
		{[]string{"users.#.name"}, "\"Bob\"\n\"Cleo\"\n"},
		{[]string{"users.#(age>30)#.name"}, "\"Cleo\"\n"},
		{[]string{"users.#(age>30).name"}, "\"Cleo\"\n"},
		{[]string{"users.#"}, "2\n"},
		{[]string{"note"}, "\"42\"\n"}, // string "42" stays quoted
		{[]string{"-r", "note"}, "42\n"},
		{[]string{"user.age"}, "36\n"},
	}
	for _, c := range cases {
		got, code := run(t, bin, doc, c.args...)
		if code != 0 || got != c.want {
			t.Errorf("args=%v: got %q (code %d), want %q", c.args, got, code, c.want)
		}
	}
}

func TestCLICompactIndentValidate(t *testing.T) {
	bin := buildCLI(t)
	doc := "{\n \"a\" : [ 1 , 2 ]\n}"
	got, code := run(t, bin, doc, "-c")
	if code != 0 || got != "{\"a\":[1,2]}\n" {
		t.Errorf("compact: %q code %d", got, code)
	}
	got, code = run(t, bin, doc, "-i")
	if code != 0 || got != "{\n  \"a\": [\n    1,\n    2\n  ]\n}\n" {
		t.Errorf("indent: %q code %d", got, code)
	}
	_, code = run(t, bin, doc, "-v")
	if code != 0 {
		t.Errorf("validate valid: code %d", code)
	}
	_, code = run(t, bin, "{bad", "-v")
	if code != 1 {
		t.Errorf("validate invalid: code %d, want 1", code)
	}
}

func TestCLINDJSON(t *testing.T) {
	bin := buildCLI(t)
	doc := "{\"k\":1}\n{\"k\":2}\n{\"k\":3}\n"
	got, code := run(t, bin, doc, "-n", "k")
	if code != 0 || got != "1\n2\n3\n" {
		t.Errorf("ndjson: %q code %d", got, code)
	}
}

func TestCLIFileAndStdin(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	f := filepath.Join(dir, "x.json")
	os.WriteFile(f, []byte(`{"a":{"b":7}}`), 0644)
	got, code := run(t, bin, "", "a.b", f)
	if code != 0 || got != "7\n" {
		t.Errorf("file: %q code %d", got, code)
	}
	got, code = run(t, bin, `{"a":{"b":8}}`, "a.b")
	if code != 0 || got != "8\n" {
		t.Errorf("stdin: %q code %d", got, code)
	}
}

func TestCLIWAL(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "s.json")
	os.WriteFile(src, []byte(`{"cfg":{"v":1},"n":5}`), 0644)
	base := filepath.Join(dir, "s.jstore")
	wal := filepath.Join(dir, "e.wal")
	if _, code := run(t, bin, "", "-seal", src, base); code != 0 {
		t.Fatalf("seal code %d", code)
	}
	// Cheap write.
	if _, code := run(t, bin, "", "-wal", wal, "-wset", base, "cfg.v", "42"); code != 0 {
		t.Fatalf("wset code %d", code)
	}
	// Overlay read.
	got, code := run(t, bin, "", "-wal", wal, "-wget", base, "cfg.v")
	if code != 0 || got != "42\n" {
		t.Errorf("wget: %q code %d", got, code)
	}
	// Delete + read missing.
	run(t, bin, "", "-wal", wal, "-wdel", base, "n")
	_, code = run(t, bin, "", "-wal", wal, "-wget", base, "n")
	if code == 0 {
		t.Error("deleted key still readable")
	}
	// Compact into a new base, then plain open reflects edits.
	comp := filepath.Join(dir, "c.jstore")
	if _, code := run(t, bin, "", "-wal", wal, "-wcompact", base, comp); code != 0 {
		t.Fatalf("wcompact code %d", code)
	}
	got, code = run(t, bin, "", "-open", comp, "cfg.v")
	if code != 0 || got != "42\n" {
		t.Errorf("compacted read: %q code %d", got, code)
	}
}