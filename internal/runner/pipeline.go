// internal/runner/pipeline.go
package runner

import (
	"context"
	"fmt"
	"sync"
	"time"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
)

// Step is one resolved cue within the execution plan.
type Step struct {
	Name             string
	ScenarioName     string // referenced scenario name — used for display/grouping
	ScenarioDesc     string
	OnErrorScenario  string // scenario whose on_error applies when this step fails;
	                        // equals ScenarioName for normal cues, parent for inline refs
	CueRef           config.CueRef
	IsLocal          bool
}

// Phase groups steps that run together (local or remote).
type Phase struct {
	Local bool
	Steps []Step
}

// SplitPhases separates steps into [localPhase, remotePhase].
// All local steps run first; remote steps run after SSH is established.
func SplitPhases(steps []Step) []Phase {
	var local, remote []Step
	for _, s := range steps {
		if s.IsLocal {
			local = append(local, s)
		} else {
			remote = append(remote, s)
		}
	}
	return []Phase{
		{Local: true, Steps: local},
		{Local: false, Steps: remote},
	}
}

// RunResult holds the outcome of all cues for one target.
type RunResult struct {
	Target          config.Target
	Results         []cue.Result
	Changed         int
	Equal           int
	Failed          int
	Elapsed         time.Duration
	Err             error
	PostActions     []cue.PostAction
	Locked          bool             // true when deploy was skipped because target was locked (on_locked: skip)
	LockReason      string           // human-readable lock skip message
	RollbackOutcome *RollbackOutcome // non-nil when on_error: rollback was triggered
}

// execFn is the type of the executor dispatch function used throughout.
type execFn = func(ctx context.Context, conn cue.SSHConn, cr config.CueRef, target config.Target) (cue.Result, error)

// ExecutePhase runs all steps in a phase.
//
// Local phases run sequentially — local actions and builds have ordering requirements
// and are CPU-bound rather than network-bound.
//
// Remote phases use a two-stage approach:
//  1. Parallel dry-run pre-check: all steps run concurrently over the shared SSH
//     connection to determine their current status (MD5 compare, file diff, etc.).
//  2. Sequential apply: only steps that are not StatusEqual are executed in order.
//     Actions always appear as StatusSkipped in dry-run and are always applied.
//
// When ctx already carries the dry-run flag (e.g. rdiff), stage 1 results are
// returned directly — no apply stage runs.
func ExecutePhase(
	ctx context.Context,
	phase Phase,
	conn cue.SSHConn,
	target config.Target,
	fn execFn,
	onResult func(cue.Result),
) ([]cue.Result, error) {
	if phase.Local {
		return executeSequential(ctx, phase.Steps, conn, target, fn, onResult)
	}
	return executeRemote(ctx, phase.Steps, conn, target, fn, onResult)
}

// executeSequential runs steps one at a time in order.
func executeSequential(
	ctx context.Context,
	steps []Step,
	conn cue.SSHConn,
	target config.Target,
	fn execFn,
	onResult func(cue.Result),
) ([]cue.Result, error) {
	var results []cue.Result
	for _, step := range steps {
		r, err := fn(ctx, conn, step.CueRef, target)
		if err != nil {
			return results, fmt.Errorf("step %s: %w", step.Name, err)
		}
		r.ScenarioName = step.ScenarioName
		r.ScenarioDesc = step.ScenarioDesc
		results = append(results, r)
		if onResult != nil {
			onResult(r)
		}
		if r.Status == cue.StatusFailed && !step.CueRef.ContinueOnError {
			return results, fmt.Errorf("cue %q failed: %v", step.Name, r.Err)
		}
	}
	return results, nil
}

// executeRemote runs a two-stage parallel-check / sequential-apply pipeline.
func executeRemote(
	ctx context.Context,
	steps []Step,
	conn cue.SSHConn,
	target config.Target,
	fn execFn,
	onResult func(cue.Result),
) ([]cue.Result, error) {
	if len(steps) == 0 {
		return nil, nil
	}

	// Stage 1: parallel pre-check — all steps with dry-run context.
	// Each executor's read-only path runs concurrently over the shared SSH connection.
	// gossh.Client.NewSession is safe for concurrent use.
	checkCtx := cue.WithDryRun(ctx)
	type checkItem struct {
		result cue.Result
		err    error
	}
	checks := make([]checkItem, len(steps))
	var wg sync.WaitGroup
	for i, step := range steps {
		i, step := i, step
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := fn(checkCtx, conn, step.CueRef, target)
			r.ScenarioName = step.ScenarioName
			r.ScenarioDesc = step.ScenarioDesc
			checks[i] = checkItem{result: r, err: err}
		}()
	}
	wg.Wait()

	// If the caller already requested dry-run (e.g. rdiff), the pre-check results
	// are the final results — no apply stage needed.
	if cue.IsDryRun(ctx) {
		results := make([]cue.Result, len(steps))
		for i, c := range checks {
			results[i] = c.result
			if onResult != nil {
				onResult(c.result)
			}
		}
		for _, c := range checks {
			if c.err != nil {
				return results, c.err
			}
		}
		return results, nil
	}

	// Stage 2: sequential apply — skip confirmed-equal steps, apply everything else.
	var results []cue.Result
	for i, step := range steps {
		c := checks[i]
		if c.err == nil && c.result.Status == cue.StatusEqual {
			// Remote already matches local — emit the pre-check result and move on.
			results = append(results, c.result)
			if onResult != nil {
				onResult(c.result)
			}
			continue
		}
		// Changed, Skipped (action), Failed pre-check, or pre-check error — apply now.
		r, err := fn(ctx, conn, step.CueRef, target)
		if err != nil {
			return results, fmt.Errorf("step %s: %w", step.Name, err)
		}
		r.ScenarioName = step.ScenarioName
		r.ScenarioDesc = step.ScenarioDesc
		results = append(results, r)
		if onResult != nil {
			onResult(r)
		}
		if r.Status == cue.StatusFailed && !step.CueRef.ContinueOnError {
			return results, fmt.Errorf("cue %q failed: %v", step.Name, r.Err)
		}
	}
	return results, nil
}
