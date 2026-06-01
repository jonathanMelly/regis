// internal/cue/when_test.go
package cue_test

import (
	"testing"
	"git.disroot.org/jmy/regis/internal/cue"
)

func TestEvalWhen_stdout_contains(t *testing.T) {
	result, err := cue.EvalWhenExpr("stdout contains Updated", "File Updated ok", "", 0)
	if err != nil || !result {
		t.Errorf("want true, got %v (err %v)", result, err)
	}
}

func TestEvalWhen_stdout_not_contains(t *testing.T) {
	result, err := cue.EvalWhenExpr("stdout !contains Already up to date.", "Already up to date.", "", 0)
	if err != nil || result {
		t.Errorf("want false (not contains), got %v (err %v)", result, err)
	}
}

func TestEvalWhen_exit_ne(t *testing.T) {
	result, err := cue.EvalWhenExpr("exit != 0", "", "", 1)
	if err != nil || !result {
		t.Errorf("want true (exit=1 != 0), got %v (err %v)", result, err)
	}
}

func TestEvalWhen_exit_eq(t *testing.T) {
	result, err := cue.EvalWhenExpr("exit == 0", "", "", 0)
	if err != nil || !result {
		t.Errorf("want true (exit=0 == 0), got %v (err %v)", result, err)
	}
}

func TestEvalWhen_unknown(t *testing.T) {
	_, err := cue.EvalWhenExpr("something unknown", "", "", 0)
	if err == nil {
		t.Error("expected error for unknown expression")
	}
}

func TestEvalWhen_stderr_contains(t *testing.T) {
	result, err := cue.EvalWhenExpr("stderr contains ERROR", "", "ERROR: disk full", 0)
	if err != nil || !result {
		t.Errorf("want true (stderr contains ERROR), got %v (err %v)", result, err)
	}
}

func TestEvalWhen_stderr_not_contains(t *testing.T) {
	result, err := cue.EvalWhenExpr("stderr !contains ERROR", "", "warning only", 0)
	if err != nil || !result {
		t.Errorf("want true (stderr !contains ERROR), got %v (err %v)", result, err)
	}
}
