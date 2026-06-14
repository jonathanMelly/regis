// cmd/regis-docgen/main_test.go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot walks up from the test's working directory until it finds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (no go.mod found)")
		}
		dir = parent
	}
}

// callGenerate is a helper that calls generate() with real source paths and a
// temp output file, returning the output content.
func callGenerate(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	out := filepath.Join(t.TempDir(), "context.md")
	err := generate(
		filepath.Join(root, "internal/config/types.go"),
		filepath.Join(root, "internal/cue"),
		filepath.Join(root, "internal/config/interpolate.go"),
		filepath.Join(root, "cmd/regis/cmd"),
		filepath.Join(root, "cmd/regis-docgen/example.yml"),
		out,
	)
	if err != nil {
		t.Fatalf("generate() returned error: %v", err)
	}
	content, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("ReadFile output: %v", err)
	}
	return string(content)
}

// TestGenerate_AllSectionsPresent calls generate() with the real source paths,
// writes to a temp file, then asserts all expected sections are present.
func TestGenerate_AllSectionsPresent(t *testing.T) {
	s := callGenerate(t)

	checks := []struct {
		label string
		text  string
	}{
		{"auto-generated header", "auto-generated"},
		{"Schema Reference heading", "## Schema Reference"},
		{"targets[] section", "### targets[]"},
		{"scenarios{} section", "### scenarios{}"},
		{"cues[] section", "### cues[]"},
		{"service cues section", "### service cues"},
		{"run section", "### run"},
		{"state section", "### state"},
		{"Nature Types heading", "## Nature Types"},
		{"binary nature", "binary"},
		{"config nature", "config"},
		{"secret nature", "secret"},
		{"action nature", "action"},
		{"Interpolation heading", "## Interpolation"},
		{"Key Concepts heading", "## Key Concepts"},
		{"requires keyword", "requires"},
		{"CLI Reference heading", "## CLI Reference"},
		{"rdiff command", "rdiff"},
		{"score command", "score"},
		{"env command", "env"},
		{"ai command", "ai"},
		{"Example regis.yml heading", "## Example regis.yml"},
		{"nature: binary in example", "nature: binary"},
	}

	for _, c := range checks {
		if !strings.Contains(s, c.text) {
			t.Errorf("output missing %s: expected to find %q", c.label, c.text)
		}
	}
}

// TestGenerate_SchemaFieldsPresent checks that known Target fields appear in the output.
func TestGenerate_SchemaFieldsPresent(t *testing.T) {
	s := callGenerate(t)

	fields := []string{"host", "dotenv", "dir", "sudo"}
	for _, f := range fields {
		if !strings.Contains(s, f) {
			t.Errorf("output missing expected Target field %q", f)
		}
	}
}

// TestGenerate_NoEmptySections asserts that no section header is immediately
// followed by another header (which would indicate an empty section).
func TestGenerate_NoEmptySections(t *testing.T) {
	s := callGenerate(t)

	lines := strings.Split(s, "\n")
	for i := 0; i+1 < len(lines); i++ {
		a := strings.TrimSpace(lines[i])
		b := strings.TrimSpace(lines[i+1])
		if strings.HasPrefix(a, "## ") && strings.HasPrefix(b, "## ") {
			t.Errorf("empty section: line %d %q immediately followed by %q", i+1, a, b)
		}
	}
}
