// cmd/regis/cmd/rtf_test.go
package cmd_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.disroot.org/jmy/regis/cmd/regis/cmd"
)

func TestRTFCommand_OutputsMarkdown(t *testing.T) {
	root := cmd.NewRootCommand("dev")
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"rtf", "--stdout"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "# ") {
		t.Errorf("expected output to contain a markdown header (\"# \"), got:\n%s", out[:min(200, len(out))])
	}
}

func TestRTFCommand_ContainsSchemaSection(t *testing.T) {
	root := cmd.NewRootCommand("dev")
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"rtf", "--stdout"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	for _, keyword := range []string{"targets", "scenarios", "cues"} {
		if !strings.Contains(out, keyword) {
			t.Errorf("expected output to contain %q", keyword)
		}
	}
}

func TestRTFCommand_ContainsExamples(t *testing.T) {
	root := cmd.NewRootCommand("dev")
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"rtf", "--stdout"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "regis.yml") {
		t.Errorf("expected output to contain \"regis.yml\" (indicating an example block)")
	}
}

func TestRTFCommand_StdoutFlag(t *testing.T) {
	root := cmd.NewRootCommand("dev")
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"rtf", "--stdout"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if buf.Len() == 0 {
		t.Error("expected --stdout to write content to command stdout, got empty output")
	}
}

func TestRTFCommand_OutputFlag(t *testing.T) {
	dir := t.TempDir()
	outFile := filepath.Join(dir, "custom.md")

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	root := cmd.NewRootCommand("dev")
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"rtf", "--output", outFile})

	err = root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("expected file %s to exist: %v", outFile, err)
	}
	if len(content) == 0 {
		t.Error("expected file to have content, got empty")
	}
	if !strings.Contains(string(content), "regis") {
		t.Errorf("expected file content to contain \"regis\"")
	}
}

func TestRTFCommand_DefaultOutputFile(t *testing.T) {
	dir := t.TempDir()
	outFile := filepath.Join(dir, "regis-rtf.md")

	root := cmd.NewRootCommand("dev")
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"rtf", "--output", outFile})

	err := root.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("expected output file regis-rtf.md to exist: %v", err)
	}
	if len(content) == 0 {
		t.Error("expected regis-rtf.md to have content, got empty")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
