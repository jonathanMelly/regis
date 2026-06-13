// internal/runner/pipeline.go
package runner

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
)

// stepWithFileProgress wraps the file-progress callback already in ctx so that
// the cueName passed to callers is prefixed with the scenario label. This lets
// the spinner show "ScenarioLabel / cueName  N/M" instead of just "cueName  N/M".
func stepWithFileProgress(ctx context.Context, step Step) context.Context {
	fn := cue.FileProgressFrom(ctx)
	if fn == nil {
		return ctx
	}
	label := step.ScenarioDesc
	if label == "" {
		label = step.ScenarioName
	}
	return cue.WithFileProgress(ctx, func(cueName string, scanned, total int) {
		fn(label+" > "+cueName, scanned, total)
	})
}

// notifyStep calls the pre-step progress callback and, in debug mode, writes a
// labelled header to the debug writer. Both are no-ops when their context keys are absent.
func notifyStep(ctx context.Context, step Step, suffix string) {
	if fn := cue.PreStepFrom(ctx); fn != nil {
		fn(step.ScenarioName, step.Name, step.ScenarioDesc)
	}
	w := cue.DebugWriterFrom(ctx)
	if w == nil {
		return
	}
	label := step.ScenarioName + " / " + step.Name
	if suffix != "" {
		label += " " + suffix
	}
	fmt.Fprintf(w, "[debug] --- %s ---\n", label)
}

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
	RestoreOutcome *RestoreOutcome // non-nil when on_error: rollback was triggered
	// SystemWarnings are non-fatal post-deploy failures (manifest write, archive, snapshot).
	// The deploy itself succeeded; these warn that release tracking may be degraded.
	SystemWarnings  []string
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
		notifyStep(ctx, step, "")
		stepCtx := stepWithFileProgress(ctx, step)
		r, err := fn(stepCtx, conn, step.CueRef, target)
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

	// Stage 1: pre-check with dry-run context.
	// Normally runs in parallel (gossh.Client.NewSession is goroutine-safe).
	// In debug mode, runs sequentially so per-step headers and SSH traces are readable.
	checkCtx := cue.WithDryRun(ctx)
	type checkItem struct {
		result cue.Result
		err    error
	}
	checks := make([]checkItem, len(steps))
	if cue.DebugWriterFrom(ctx) != nil {
		for i, step := range steps {
			notifyStep(ctx, step, "")
			stepCtx := stepWithFileProgress(checkCtx, step)
			r, err := fn(stepCtx, conn, step.CueRef, target)
			r.ScenarioName = step.ScenarioName
			r.ScenarioDesc = step.ScenarioDesc
			checks[i] = checkItem{result: r, err: err}
			if fn := cue.CheckResultFrom(checkCtx); fn != nil {
				fn(r)
			}
		}
	} else {
		if fn := cue.PrePhaseFrom(checkCtx); fn != nil {
			infos := make([]cue.StepInfo, len(steps))
			for i, s := range steps {
				infos[i] = cue.StepInfo{Name: s.Name, ScenarioName: s.ScenarioName, ScenarioDesc: s.ScenarioDesc}
			}
			fn(infos)
		}
		var wg sync.WaitGroup
		var checked int32
		total := len(steps)
		for i, step := range steps {
			i, step := i, step
			wg.Add(1)
			go func() {
				defer wg.Done()
				notifyStep(checkCtx, step, "")
				stepCtx := stepWithFileProgress(checkCtx, step)
				r, err := fn(stepCtx, conn, step.CueRef, target)
				r.ScenarioName = step.ScenarioName
				r.ScenarioDesc = step.ScenarioDesc
				checks[i] = checkItem{result: r, err: err}
				if fn := cue.CheckResultFrom(checkCtx); fn != nil {
					fn(r)
				}
				n := int(atomic.AddInt32(&checked, 1))
				if fn := cue.CueProgressFrom(checkCtx); fn != nil {
					fn(n, total)
				}
			}()
		}
		wg.Wait()
	}

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
		notifyStep(ctx, step, "(apply)")
		stepCtx := stepWithFileProgress(ctx, step)
		r, err := fn(stepCtx, conn, step.CueRef, target)
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
