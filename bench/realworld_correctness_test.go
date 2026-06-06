package bench

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/shiahonb777/jseek"
)

// Validate that the generated fixtures are valid JSON and that jseek extracts the
// expected values, so the benchmarks measure correct work.

func TestGitHubFixtureValid(t *testing.T) {
	if !json.Valid(githubFixture) {
		t.Fatal("github fixture is not valid JSON")
	}
	if s, _ := jseek.GetString(githubFixture, "full_name"); s != "octocat/Hello-World" {
		t.Fatalf("full_name = %q", s)
	}
	if s, _ := jseek.GetString(githubFixture, "owner", "login"); s != "octocat" {
		t.Fatalf("owner.login = %q", s)
	}
	if s, _ := jseek.GetString(githubFixture, "issues", "[100]", "user", "login"); s != "user100" {
		t.Fatalf("issues[100].user.login = %q", s)
	}
	if s, _ := jseek.GetString(githubFixture, "issues", "[199]", "labels", "[1]", "name"); s != "help wanted" {
		t.Fatalf("issues[199].labels[1].name = %q", s)
	}

	// The tape engine must agree with stateless extraction.
	d := jseek.IndexTape(githubFixture)
	if s, _ := d.GetString("issues", "[199]", "labels", "[1]", "name"); s != "help wanted" {
		t.Fatalf("tape issues[199].labels[1].name = %q", s)
	}
}

func TestNDJSONFixtureValid(t *testing.T) {
	lines := strings.Split(string(ndjsonFixture), "\n")
	count := 0
	for _, line := range lines {
		if line == "" {
			continue
		}
		count++
		if !json.Valid([]byte(line)) {
			t.Fatalf("ndjson line %d not valid: %s", count, line)
		}
	}
	if count != 5000 {
		t.Fatalf("expected 5000 records, got %d", count)
	}

	// Spot-check a streamed record.
	dec := jseek.NewDecoder(strings.NewReader(string(ndjsonFixture)))
	first, err := dec.Next()
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := jseek.GetString(first, "method"); m != "GET" {
		t.Fatalf("first.method = %q", m)
	}
	if r, _ := jseek.GetString(first, "client", "region"); r != "us-east-1" {
		t.Fatalf("first.client.region = %q", r)
	}
}
