// internal/runner/rollback.go
package runner

import (
	"context"
	"fmt"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
)

// RestoreOutcome records what happened during an automatic restore triggered by on_error: restore.
type RestoreOutcome struct {
	Results []cue.Result // outcomes of restore action cues
	Err     error
}

// executeRestore runs per-cue action compensation and scenario-level restore cues.
// File restoration is the user's responsibility (git worktree + redeploy).
// Only action/service compensation shells and scenario restore: blocks run here.
func executeRestore(
	ctx context.Context,
	conn cue.SSHConn,
	cfg *config.Config,
	order []string,
	deployID string, // ID of the failed deploy — available as $DEPLOY_ID in restore actions
	target config.Target,
	dispatch Dispatch,
	onResult func(cue.Result),
	deployResults []cue.Result,
	deploySteps []Step,
) *RestoreOutcome {
	out := &RestoreOutcome{}

	// Collect cues with restore: enabled in reverse execution order.
	type cuePair struct {
		step   Step
		result cue.Result
	}
	var cuePairs []cuePair
	for i, result := range deployResults {
		if i >= len(deploySteps) {
			break
		}
		step := deploySteps[i]
		if result.Status == cue.StatusSkipped || result.Status == cue.StatusRunning {
			continue
		}
		cr := step.CueRef
		if cr.Restore == nil || !cr.Restore.Enabled {
			continue
		}
		cuePairs = append(cuePairs, cuePair{step: step, result: result})
	}

	// Partition: regular compensations (reverse order) vs deferred re-runs (forward).
	var regularPairs, deferredPairs []cuePair
	for _, p := range cuePairs {
		if p.step.CueRef.Restore.Defer {
			deferredPairs = append(deferredPairs, p)
		} else {
			regularPairs = append(regularPairs, p)
		}
	}

	// Regular compensations in reverse execution order (action/service shells only).
	// File restoration is not automated — use `regis state hint` for guidance.
	for i := len(regularPairs) - 1; i >= 0; i-- {
		cr := regularPairs[i].step.CueRef
		if cr.Restore.Shell == "" {
			continue // file natures without a shell: no automated restore
		}
		sudo := cr.Restore.Sudo || cr.Sudo || target.Sudo
		var runErr error
		if sudo {
			_, _, _, runErr = conn.RunSudo(cr.Restore.Shell)
		} else {
			_, _, _, runErr = conn.Run(cr.Restore.Shell)
		}
		if runErr != nil {
			out.Err = fmt.Errorf("restore: cue %q compensation: %w", cr.Name, runErr)
			return out
		}
	}

	// Deferred cues re-run in original forward order, after compensations complete.
	for _, p := range deferredPairs {
		cr := p.step.CueRef
		if cr.Shell == "" {
			continue
		}
		sudo := cr.Sudo || target.Sudo
		var runErr error
		if sudo {
			_, _, _, runErr = conn.RunSudo(cr.Shell)
		} else {
			_, _, _, runErr = conn.Run(cr.Shell)
		}
		if runErr != nil {
			out.Err = fmt.Errorf("restore: deferred %q: %w", cr.Name, runErr)
			return out
		}
	}

	// Scenario-level restore: action cues in topo order.
	baseEnv := map[string]string{"DEPLOY_ID": deployID}
	var restoreSteps []Step
	for _, scName := range order {
		sc := cfg.Scenarios[scName]
		for _, cr := range sc.Restore {
			if cr.ScenarioRef != "" {
				continue
			}
			cr.Nature = "action"
			cr.Env = mergeEnv(baseEnv, cfg.Defaults.Env, sc.Env, cr.Env)
			restoreSteps = append(restoreSteps, Step{
				Name:            cr.Name,
				ScenarioName:    scName,
				ScenarioDesc:    sc.Describe,
				OnErrorScenario: scName,
				CueRef:          cr,
			})
		}
	}
	if len(restoreSteps) > 0 {
		results, err := executeSequential(ctx, restoreSteps, conn, target, execWith(dispatch), onResult)
		out.Results = results
		if err != nil {
			out.Err = fmt.Errorf("restore action failed: %w", err)
		}
	}
	return out
}

// effectiveOnError returns the on_error policy for a failing scenario.
// Priority: scenario.on_error → inferred from per-cue restore: fields → cfg.Run.OnError → "halt".
func effectiveOnError(cfg *config.Config, failingScenario string) string {
	if sc, ok := cfg.Scenarios[failingScenario]; ok {
		if sc.OnError != "" {
			return sc.OnError
		}
		for _, cr := range sc.Cues {
			if cr.ScenarioRef == "" && cr.Restore != nil && cr.Restore.Enabled {
				return "restore"
			}
		}
	}
	if cfg.Run.OnError != "" {
		return cfg.Run.OnError
	}
	return "halt"
}

// failingScenarioName returns the OnErrorScenario of the first StatusFailed result.
func failingScenarioName(results []cue.Result, steps []Step) string {
	for i, r := range results {
		if r.Status == cue.StatusFailed && i < len(steps) {
			return steps[i].OnErrorScenario
		}
	}
	return ""
}
