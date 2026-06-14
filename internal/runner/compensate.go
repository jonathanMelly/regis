// internal/runner/compensate.go
package runner

import (
	"context"
	"fmt"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
)

// CompensateOutcome records what happened during automatic compensation triggered by on_error: compensate.
type CompensateOutcome struct {
	Results []cue.Result // outcomes of scenario-level compensate: action cues
	Err     error
}

// executeCompensation runs per-cue compensation shells and scenario-level compensate: cues.
// File state is not automatically restored — use `regis state hint` for guidance.
//
// Execution order:
//  1. Regular per-cue compensations in reverse execution order (action/service shells).
//  2. Deferred per-cue compensations in original forward order (after all regulars complete).
//  3. Scenario-level compensate: action cues in topo order.
//
// When ui is non-nil each step prompts the operator before running. A nil ui auto-runs
// every step (CI / non-interactive mode).
func executeCompensation(
	ctx context.Context,
	conn cue.SSHConn,
	cfg *config.Config,
	order []string,
	deployID string,
	target config.Target,
	dispatch Dispatch,
	onResult func(cue.Result),
	deployResults []cue.Result,
	deploySteps []Step,
	ui CompensateUI,
) *CompensateOutcome {
	out := &CompensateOutcome{}

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
		if cr.Compensation == nil || !cr.Compensation.Enabled {
			continue
		}
		cuePairs = append(cuePairs, cuePair{step: step, result: result})
	}

	var regularPairs, deferredPairs []cuePair
	for _, p := range cuePairs {
		if p.step.CueRef.Compensation.Defer {
			deferredPairs = append(deferredPairs, p)
		} else {
			regularPairs = append(regularPairs, p)
		}
	}

	// Regular compensations in reverse execution order.
	for i := len(regularPairs) - 1; i >= 0; i-- {
		cr := regularPairs[i].step.CueRef
		if cr.Compensation.Shell == "" && !cr.Compensation.Interactive {
			continue // file nature with compensation: true — no shell, skip silently
		}
		if err := runOneCompensation(conn, cr, target, ui); err != nil {
			out.Err = err
			return out
		}
	}

	// Deferred compensations in original forward order, after regulars complete.
	for _, p := range deferredPairs {
		cr := p.step.CueRef
		shell := cr.Shell // deferred: re-run the cue's own shell
		if shell == "" {
			continue
		}
		if err := runDeferredCompensation(conn, cr, shell, target, ui); err != nil {
			out.Err = err
			return out
		}
	}

	// Scenario-level compensate: action cues in topo order.
	baseEnv := map[string]string{"STATE_ID": deployID}
	var compensateSteps []Step
	for _, scName := range order {
		sc := cfg.Scenarios[scName]
		for _, cr := range sc.Compensate {
			if cr.ScenarioRef != "" {
				continue
			}
			cr.Nature = "action"
			cr.Env = mergeEnv(baseEnv, cfg.Defaults.Env, sc.Env, cr.Env)
			compensateSteps = append(compensateSteps, Step{
				Name:            cr.Name,
				ScenarioName:    scName,
				ScenarioDesc:    sc.Describe,
				OnErrorScenario: scName,
				CueRef:          cr,
			})
		}
	}
	if len(compensateSteps) > 0 {
		results, err := executeSequential(ctx, compensateSteps, conn, target, execWith(dispatch), onResult)
		out.Results = results
		if err != nil {
			out.Err = fmt.Errorf("compensate: action failed: %w", err)
		}
	}
	return out
}

// runOneCompensation runs a single regular compensation shell (or prompts via ui).
func runOneCompensation(conn cue.SSHConn, cr config.CueRef, target config.Target, ui CompensateUI) error {
	comp := cr.Compensation
	if ui != nil {
		choice := ui.Prompt(cr.Name, comp.Shell)
		switch choice {
		case CompensateSkip:
			return nil
		case CompensateStop:
			return errCompensationStopped
		case CompensateShell:
			return ui.OpenShell(target)
		}
		// CompensateRun: fall through to execute
	}
	sudo := comp.Sudo || cr.Sudo || target.Sudo
	shell := comp.Shell
	if comp.Interactive {
		return ui.OpenShell(target) // interactive: always open shell (ui guaranteed non-nil here by caller logic)
	}
	var runErr error
	if sudo {
		_, _, _, runErr = conn.RunSudo(shell)
	} else {
		_, _, _, runErr = conn.Run(shell)
	}
	if runErr != nil {
		return fmt.Errorf("compensate: cue %q: %w", cr.Name, runErr)
	}
	return nil
}

// runDeferredCompensation runs a deferred compensation (re-runs the cue's own shell).
func runDeferredCompensation(conn cue.SSHConn, cr config.CueRef, shell string, target config.Target, ui CompensateUI) error {
	if ui != nil {
		choice := ui.Prompt(cr.Name, shell)
		switch choice {
		case CompensateSkip:
			return nil
		case CompensateStop:
			return errCompensationStopped
		case CompensateShell:
			return ui.OpenShell(target)
		}
	}
	sudo := cr.Sudo || target.Sudo
	var runErr error
	if sudo {
		_, _, _, runErr = conn.RunSudo(shell)
	} else {
		_, _, _, runErr = conn.Run(shell)
	}
	if runErr != nil {
		return fmt.Errorf("compensate: deferred %q: %w", cr.Name, runErr)
	}
	return nil
}

// InferCompensateEnabled returns true when the overall inferred on_error policy is
// "compensate" for the set of scenarios: at least one cue has compensation enabled
// (or the scenario/global config sets on_error: compensate), unless any scenario
// explicitly overrides on_error: halt.
func InferCompensateEnabled(cfg *config.Config, scenarioNames []string) bool {
	hasComp := false
	for _, name := range scenarioNames {
		sc, ok := cfg.Scenarios[name]
		if !ok {
			continue
		}
		switch sc.OnError {
		case "halt":
			return false
		case "compensate":
			hasComp = true
			continue
		}
		for _, cr := range sc.Cues {
			if cr.ScenarioRef == "" && cr.Compensation != nil && cr.Compensation.Enabled {
				hasComp = true
			}
		}
	}
	if hasComp {
		return true
	}
	return cfg.Run.OnError == "compensate"
}

// effectiveOnError returns the on_error policy for a failing scenario.
// Priority: scenario.on_error → inferred from per-cue compensation: fields → cfg.Run.OnError → "halt".
// "restore" is accepted as an alias for "compensate" for backward compatibility.
func effectiveOnError(cfg *config.Config, failingScenario string) string {
	normalize := func(s string) string {
		if s == "restore" {
			return "compensate"
		}
		return s
	}
	if sc, ok := cfg.Scenarios[failingScenario]; ok {
		if sc.OnError != "" {
			return normalize(sc.OnError)
		}
		for _, cr := range sc.Cues {
			if cr.ScenarioRef == "" && cr.Compensation != nil && cr.Compensation.Enabled {
				return "compensate"
			}
		}
	}
	if cfg.Run.OnError != "" {
		return normalize(cfg.Run.OnError)
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
