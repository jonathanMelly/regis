// internal/cue/types_test.go
package cue_test

import (
	"testing"
	"git.disroot.org/jmy/regis/internal/cue"
)

func TestStatus_strings(t *testing.T) {
	cases := map[cue.Status]string{
		cue.StatusEqual:   "=",
		cue.StatusChanged: "~",
		cue.StatusFailed:  "FAILED",
		cue.StatusSkipped: "skipped",
		cue.StatusRunning: "...",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("Status(%d).String() = %q, want %q", s, got, want)
		}
	}
}

func TestApplied_changedSaysDeployed(t *testing.T) {
	if got := cue.StatusChanged.Applied(); got != "deployed" {
		t.Errorf("StatusChanged.Applied() = %q, want \"deployed\"", got)
	}
}

func TestApplied_othersPassThrough(t *testing.T) {
	for _, s := range []cue.Status{cue.StatusEqual, cue.StatusFailed, cue.StatusSkipped} {
		if got, want := s.Applied(), s.String(); got != want {
			t.Errorf("Status(%d).Applied() = %q, want %q", s, got, want)
		}
	}
}

func TestResult_isStateAffecting(t *testing.T) {
	r := cue.Result{Nature: "binary", Status: cue.StatusChanged}
	if !r.IsStateAffecting() {
		t.Error("binary changed cue must be state-affecting")
	}
	r2 := cue.Result{Nature: "action", Status: cue.StatusChanged, AffectsState: false}
	if r2.IsStateAffecting() {
		t.Error("remote action without affects_state must not be state-affecting")
	}
}
