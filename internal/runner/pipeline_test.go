// internal/runner/pipeline_test.go
package runner_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
	"git.disroot.org/jmy/regis/internal/runner"
)

// TestExecutePhase_parallelPreCheck verifies that remote steps are pre-checked
// concurrently and that equal cues are not re-executed in the apply stage.
func TestExecutePhase_parallelPreCheck(t *testing.T) {
	// Track how many times each step's execFn is called and the peak concurrency.
	var callCount [3]atomic.Int32
	var concurrency atomic.Int32
	var peakConcurrency atomic.Int32

	steps := []runner.Step{
		{Name: "a", ScenarioName: "s", CueRef: config.CueRef{Name: "a", Nature: "binary"}},
		{Name: "b", ScenarioName: "s", CueRef: config.CueRef{Name: "b", Nature: "binary"}},
		{Name: "c", ScenarioName: "s", CueRef: config.CueRef{Name: "c", Nature: "binary"}},
	}
	// Step "a" and "b" will return Equal in dry-run; "c" will return Changed.
	execFn := func(ctx context.Context, conn cue.SSHConn, cr config.CueRef, _ config.Target) (cue.Result, error) {
		idx := map[string]int{"a": 0, "b": 1, "c": 2}[cr.Name]
		callCount[idx].Add(1)

		cur := concurrency.Add(1)
		for {
			peak := peakConcurrency.Load()
			if cur <= peak || peakConcurrency.CompareAndSwap(peak, cur) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond) // simulate SSH round-trip
		concurrency.Add(-1)

		if cue.IsDryRun(ctx) {
			if cr.Name == "c" {
				return cue.Result{CueName: cr.Name, Status: cue.StatusChanged}, nil
			}
			return cue.Result{CueName: cr.Name, Status: cue.StatusEqual}, nil
		}
		return cue.Result{CueName: cr.Name, Status: cue.StatusChanged}, nil
	}

	phase := runner.Phase{Local: false, Steps: steps}
	results, err := runner.ExecutePhase(context.Background(), phase, nil, config.Target{}, execFn, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}

	// Peak concurrency during pre-check must be > 1 (all 3 fired simultaneously).
	if peakConcurrency.Load() < 2 {
		t.Errorf("expected parallel pre-check (peak concurrency >= 2), got %d", peakConcurrency.Load())
	}

	// "a" and "b" were Equal in pre-check → called once (pre-check only, no apply).
	if n := callCount[0].Load(); n != 1 {
		t.Errorf("step a: want 1 call (pre-check only), got %d", n)
	}
	if n := callCount[1].Load(); n != 1 {
		t.Errorf("step b: want 1 call (pre-check only), got %d", n)
	}
	// "c" was Changed in pre-check → called twice (pre-check + apply).
	if n := callCount[2].Load(); n != 2 {
		t.Errorf("step c: want 2 calls (pre-check + apply), got %d", n)
	}

	// Result order must match step order.
	for i, r := range results {
		if r.CueName != steps[i].Name {
			t.Errorf("result[%d]: want cue %q, got %q", i, steps[i].Name, r.CueName)
		}
	}
}

func TestPhase_local_before_remote(t *testing.T) {
	phases := runner.SplitPhases([]runner.Step{
		{Name: "go", IsLocal: true},
		{Name: "bins", IsLocal: true},
		{Name: "upload", IsLocal: false},
		{Name: "restart", IsLocal: false},
	})
	if len(phases) != 2 {
		t.Fatalf("want 2 phases, got %d", len(phases))
	}
	for _, s := range phases[0].Steps {
		if !s.IsLocal {
			t.Errorf("phase 0 must contain only local steps; got remote: %s", s.Name)
		}
	}
	for _, s := range phases[1].Steps {
		if s.IsLocal {
			t.Errorf("phase 1 must contain only remote steps; got local: %s", s.Name)
		}
	}
}
