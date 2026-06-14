// internal/cue/generate_test.go
package cue_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
)

func TestGenerateExecutor_default_status_equal(t *testing.T) {
	ex := cue.NewGenerateExecutor()
	cr := config.CueRef{
		Name:   "make-conf",
		Nature: "generate",
		Shell:  "echo hello",
	}
	r, err := ex.Execute(context.Background(), nil, cr, config.Target{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != cue.StatusEqual {
		t.Errorf("generate default status: got %v, want StatusEqual", r.Status)
	}
}

func TestGenerateExecutor_is_local(t *testing.T) {
	ex := cue.NewGenerateExecutor()
	cr := config.CueRef{Name: "gen", Nature: "generate", Shell: "echo x"}
	r, _ := ex.Execute(context.Background(), nil, cr, config.Target{})
	if !r.IsLocal {
		t.Error("generate result must have IsLocal = true")
	}
}

func TestGenerateExecutor_nature_field(t *testing.T) {
	ex := cue.NewGenerateExecutor()
	cr := config.CueRef{Name: "gen", Nature: "generate", Shell: "echo x"}
	r, _ := ex.Execute(context.Background(), nil, cr, config.Target{})
	if r.Nature != "generate" {
		t.Errorf("want Nature=generate, got %q", r.Nature)
	}
}

func TestGenerateExecutor_runs_in_dryrun_context(t *testing.T) {
	ex := cue.NewGenerateExecutor()
	cr := config.CueRef{Name: "gen", Nature: "generate", Shell: "echo produced"}
	ctx := cue.WithCheckOnly(context.Background())
	r, err := ex.Execute(ctx, nil, cr, config.Target{})
	if err != nil {
		t.Fatal(err)
	}
	// Must execute (not skip) — dry-run context must not suppress generation
	if r.Status == cue.StatusSkipped {
		t.Error("generate must not be skipped in dry-run context")
	}
	if r.Stdout == "" {
		t.Error("generate must have run and produced stdout even in dry-run")
	}
}

func TestGenerateExecutor_changed_when_true(t *testing.T) {
	ex := cue.NewGenerateExecutor()
	tr := true
	cr := config.CueRef{
		Name:        "gen",
		Nature:      "generate",
		Shell:       "echo x",
		ChangedWhen: config.WhenExpr{BoolLiteral: &tr},
	}
	r, _ := ex.Execute(context.Background(), nil, cr, config.Target{})
	if r.Status != cue.StatusChanged {
		t.Errorf("changed_when: true must yield StatusChanged, got %v", r.Status)
	}
}

func TestGenerateExecutor_nonzero_exit_fails(t *testing.T) {
	ex := cue.NewGenerateExecutor()
	cr := config.CueRef{
		Name:   "gen",
		Nature: "generate",
		Shell:  "exit 1",
	}
	r, _ := ex.Execute(context.Background(), nil, cr, config.Target{})
	if r.Status != cue.StatusFailed {
		t.Errorf("non-zero exit must yield StatusFailed, got %v", r.Status)
	}
}

// TestGenerateExecutor_uses_local_dir verifies that the generate shell runs with its
// CWD set to the directory stored in WithLocalDir (cfg.BaseDir).
func TestGenerateExecutor_uses_local_dir(t *testing.T) {
	dir := t.TempDir()
	ex := cue.NewGenerateExecutor()
	cr := config.CueRef{Name: "pwd-gen", Nature: "generate", Shell: "pwd"}
	ctx := cue.WithLocalDir(context.Background(), dir)
	r, err := ex.Execute(ctx, nil, cr, config.Target{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status == cue.StatusFailed {
		t.Fatalf("unexpected failure: %v", r.Err)
	}
	want, _ := filepath.EvalSymlinks(dir)
	got, _ := filepath.EvalSymlinks(strings.TrimSpace(r.Stdout))
	if got != want {
		t.Errorf("generate shell CWD = %q, want %q", got, want)
	}
}

func TestGenerateExecutor_not_state_affecting_when_changed(t *testing.T) {
	// Even if changed_when: true causes StatusChanged, generate is never state-affecting
	ex := cue.NewGenerateExecutor()
	tr := true
	cr := config.CueRef{
		Name:        "gen",
		Nature:      "generate",
		Shell:       "echo x",
		ChangedWhen: config.WhenExpr{BoolLiteral: &tr},
	}
	r, _ := ex.Execute(context.Background(), nil, cr, config.Target{})
	if r.IsStateAffecting() {
		t.Error("generate must never be state-affecting")
	}
}
