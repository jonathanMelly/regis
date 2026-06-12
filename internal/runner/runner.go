// internal/runner/runner.go
package runner

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"strings"
	"time"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
	"git.disroot.org/jmy/regis/internal/manager"
	regssh "git.disroot.org/jmy/regis/internal/ssh"
)

// Options controls runner behavior.
type Options struct {
	DryRun           bool
	SkipConfirm      bool
	NatureFilter     []string            // empty = all natures
	PruneReleases    bool                // prune old releases (remote + local) after a successful deploy
	DeduplicateSteps bool                // drop duplicate (ScenarioName, CueName) steps — rdiff only
	ScenarioFilter   []string            // if non-empty, keep only steps whose ScenarioName is in this set — rdiff only
	CueFilter        []string            // if non-empty, keep only steps whose Name is in this set — rdiff only
	ScopedCues       map[string][]string // scenario → cue names; keep only those cues, error if any are not found
}

// Dispatch maps cue nature to executor.
type Dispatch struct {
	Binary   cue.Executor
	Config   cue.Executor
	Secret   cue.Executor
	Action   cue.Executor
	Generate cue.Executor
	Render   cue.Executor
	Pack     cue.Executor
	Service  cue.Executor
}

// Run executes a list of scenario names against one target.
// After a successful deploy (any StatusChanged release-affecting cue), writes .regis-release.
func Run(ctx context.Context, cfg *config.Config, scenarioNames []string, target config.Target, opts Options, dispatch Dispatch, onResult func(cue.Result)) (*RunResult, error) {
	start := time.Now()

	order, err := TopoSort(cfg.Scenarios, scenarioNames)
	if err != nil {
		return nil, fmt.Errorf("topo sort: %w", err)
	}

	// Generate release ID now so ${RELEASE_ID} is consistent across all cues
	// and matches any pre-deploy backup labels (e.g. backup --label=pre-${RELEASE_ID}).
	releaseID := NewReleaseID()
	baseEnv := map[string]string{"RELEASE_ID": releaseID}

	var steps []Step
	for _, scName := range order {
		sc := cfg.Scenarios[scName]
		for _, cr := range sc.Cues {
			if cr.ScenarioRef != "" {
				steps = append(steps, expandScenarioRef(cr, cfg, baseEnv, opts.NatureFilter, scName, 0)...)
				continue
			}
			if len(opts.NatureFilter) > 0 && !natureMatches(opts.NatureFilter, cr.Nature) {
				continue
			}
			cr.Env = mergeEnv(baseEnv, cfg.Defaults.Env, sc.Env, cr.Env)
			steps = append(steps, Step{
				Name:            cr.Name,
				ScenarioName:    scName,
				ScenarioDesc:    sc.Describe,
				OnErrorScenario: scName,
				CueRef:          cr,
				IsLocal:         cr.Local || cr.Nature == "generate",
			})
		}
	}

	if opts.DeduplicateSteps {
		seen := make(map[string]bool, len(steps))
		deduped := make([]Step, 0, len(steps))
		for _, s := range steps {
			key := s.ScenarioName + "\x00" + s.Name
			if seen[key] {
				continue
			}
			seen[key] = true
			deduped = append(deduped, s)
		}
		steps = deduped
	}

	if len(opts.ScopedCues) > 0 {
		// Build the set of explicitly-requested scenario names so that topo-sorted
		// dependency scenarios (not named by the caller) are NOT kept when scoped
		// syntax is in use. Unscoped but explicitly-requested scenarios run fully;
		// deps pulled in by topo-sort are dropped.
		explicitScenarios := make(map[string]bool, len(scenarioNames))
		for _, n := range scenarioNames {
			explicitScenarios[n] = true
		}
		matched := make(map[string]bool)
		filtered := make([]Step, 0, len(steps))
		for _, s := range steps {
			cues, ok := opts.ScopedCues[s.ScenarioName]
			if !ok {
				// Keep only if explicitly requested; drop topo-sorted deps.
				if explicitScenarios[s.ScenarioName] {
					filtered = append(filtered, s)
				}
				continue
			}
			for _, n := range cues {
				if n == s.Name {
					filtered = append(filtered, s)
					matched[s.ScenarioName+"\x00"+n] = true
					break
				}
			}
		}
		for scName, cues := range opts.ScopedCues {
			for _, cueName := range cues {
				if !matched[scName+"\x00"+cueName] {
					return nil, fmt.Errorf("scenario %q has no cue %q", scName, cueName)
				}
			}
		}
		steps = filtered
	}

	if len(opts.ScenarioFilter) > 0 || len(opts.CueFilter) > 0 {
		scSet := make(map[string]bool, len(opts.ScenarioFilter))
		for _, s := range opts.ScenarioFilter {
			scSet[s] = true
		}
		cueSet := make(map[string]bool, len(opts.CueFilter))
		for _, c := range opts.CueFilter {
			cueSet[c] = true
		}
		filtered := make([]Step, 0, len(steps))
		for _, s := range steps {
			if scSet[s.ScenarioName] || cueSet[s.Name] {
				filtered = append(filtered, s)
			}
		}
		steps = filtered
	}

	phases := SplitPhases(steps)
	localPhase := phases[0]
	remotePhase := phases[1]

	rr := &RunResult{Target: target}

	// PHASE 1a: generate cues — always run, even in dry-run.
	// They produce artifacts (e.g. rendered config files) that downstream config cues
	// compare against the remote during rdiff. Running them unconditionally avoids a
	// stale-file race in the parallel pre-check.
	var genSteps, otherLocalSteps []Step
	for _, s := range localPhase.Steps {
		if s.CueRef.Nature == "generate" {
			genSteps = append(genSteps, s)
		} else {
			otherLocalSteps = append(otherLocalSteps, s)
		}
	}
	if len(genSteps) > 0 {
		results, err := ExecutePhase(ctx, Phase{Local: true, Steps: genSteps}, nil, target, execWith(dispatch), onResult)
		rr.Results = append(rr.Results, results...)
		tallyCounts(rr, results)
		if err != nil {
			rr.Err = err
			rr.Elapsed = time.Since(start)
			return rr, nil
		}
	}

	// PHASE 1b: other local cues (local: true actions) — skipped in dry-run.
	if !opts.DryRun && len(otherLocalSteps) > 0 {
		results, err := ExecutePhase(ctx, Phase{Local: true, Steps: otherLocalSteps}, nil, target, execWith(dispatch), onResult)
		rr.Results = append(rr.Results, results...)
		tallyCounts(rr, results)
		if err != nil {
			rr.Err = err
			rr.Elapsed = time.Since(start)
			return rr, nil
		}
	}

	// PHASE 2: remote (SSH required)
	// In dry-run mode: executors use their pre-dialed e.conn; no second dial needed.
	// Pass nil as conn — executors ignore it (they use e.conn). DryRun context tells
	// them to compare only and skip uploads.
	if opts.DryRun {
		ctx = cue.WithDryRun(ctx)
		if len(remotePhase.Steps) > 0 {
			results, _ := ExecutePhase(ctx, remotePhase, nil, target, execWith(dispatch), onResult)
			rr.Results = append(rr.Results, results...)
			tallyCounts(rr, results)
		}
		rr.Elapsed = time.Since(start)
		return rr, nil
	}

	conn, err := regssh.Dial(target)
	if err != nil {
		rr.Err = fmt.Errorf("SSH connect: %w", err)
		rr.Elapsed = time.Since(start)
		return rr, rr.Err
	}
	defer conn.Close()

	// Concurrency lock — acquire before touching the target.
	if cfg.Concurrency.Lock {
		lp := targetLockPath(target)
		if err := acquireLock(conn, lp, cfg.Concurrency); err != nil {
			if errors.Is(err, errSkippedLocked) {
				rr.Locked = true
				rr.LockReason = fmt.Sprintf("target is locked (%s) — skipped (on_locked: skip)", lp)
				rr.Elapsed = time.Since(start)
				return rr, nil
			}
			rr.Err = err
			rr.Elapsed = time.Since(start)
			return rr, rr.Err
		}
		defer releaseLock(conn, lp)
	}

	// Run deploy-level pre: commands
	for _, pp := range cfg.Pre {
		if err := runPrePost(ctx, pp, conn); err != nil {
			rr.Err = fmt.Errorf("pre: %w", err)
			rr.Elapsed = time.Since(start)
			return rr, rr.Err
		}
	}

	results, phaseErr := ExecutePhase(ctx, remotePhase, conn, target, execWith(dispatch), onResult)
	rr.Results = append(rr.Results, results...)
	tallyCounts(rr, results)
	if phaseErr != nil {
		// Check on_error policy for the failing scenario.
		failSc := failingScenarioName(results, remotePhase.Steps)
		if effectiveOnError(cfg, failSc) == "rollback" {
			localDir := cfg.Release.LocalDir
			if localDir == "" {
				localDir = ".regis-releases"
			}
			rr.RollbackOutcome = executeRollback(ctx, conn, cfg, order, releaseID, localDir, target, dispatch, onResult, results, remotePhase.Steps)
		}
		rr.Err = phaseErr
		rr.Elapsed = time.Since(start)
		return rr, nil
	}

	// Collect post-actions from changed cues + scenario-level posts.
	// Scenario post fires once if any remote cue in that scenario changed.
	var allPost []cue.PostAction
	scenarioChanged := make(map[string]bool)
	for i, r := range results {
		if r.Status == cue.StatusChanged {
			allPost = append(allPost, r.PostActions...)
			if i < len(remotePhase.Steps) {
				scenarioChanged[remotePhase.Steps[i].ScenarioName] = true
			}
		}
	}
	for _, scName := range order {
		if scenarioChanged[scName] {
			sc := cfg.Scenarios[scName]
			if sc.Post.Cmd != "" {
				allPost = append(allPost, cue.PostAction{Cmd: sc.Post.Cmd, Sudo: sc.Post.Sudo})
			}
		}
	}
	rr.PostActions = DeduplicatePostActions(allPost)

	// Execute post-actions, expanding restart:<svc> / reload:<svc> shorthands.
	for _, pa := range rr.PostActions {
		shellCmd, sudo := resolvePostAction(pa, cfg, target)
		var runErr error
		if sudo {
			_, _, _, runErr = conn.RunSudo(shellCmd)
		} else {
			_, _, _, runErr = conn.Run(shellCmd)
		}
		if runErr != nil {
			rr.Err = fmt.Errorf("post-action %q: %w", shellCmd, runErr)
			rr.Elapsed = time.Since(start)
			return rr, rr.Err
		}
	}

	// Run deploy-level post: commands (non-fatal: errors are logged but do not fail the deploy)
	for _, pp := range cfg.Post {
		_ = runPrePost(ctx, pp, conn)
	}

	// Write release manifest + local snapshot + remote archive if any release-affecting cue changed.
	// Release tracking is enabled by default (assigns a release ID per deploy for rollback).
	// Disable with release.enabled: false for live deploys with no history.
	releaseEnabled := cfg.Release.Enabled == nil || *cfg.Release.Enabled
	if releaseEnabled {
		for _, r := range rr.Results {
			if r.IsReleaseAffecting() {
				manifest := BuildManifest(releaseID, scenarioNames, rr.Results, remotePhase.Steps, target.Dir)
				// Non-fatal: manifest write failure does not fail the deploy.
				_ = WriteManifest(conn, target.Dir, manifest, target.Sudo)
				localDir := cfg.Release.LocalDir
				if localDir == "" {
					localDir = ".regis-releases"
				}
				SnapshotRelease(localDir, releaseID, manifest, remotePhase.Steps, rr.Results)
				// Archive deployed files on remote via cp (no re-upload).
				// Default remote release dir to <target.dir>/.regis-releases when not configured.
				releaseDir := cfg.Release.Dir
				if releaseDir == "" {
					releaseDir = path.Join(target.Dir, ".regis-releases")
				}
				archiveCmd := fmt.Sprintf("mkdir -p %s && cp -rp %s/. %s/%s/",
					releaseDir, target.Dir, releaseDir, releaseID)
				_, _, _, _ = conn.Run(archiveCmd)
				// Prune old releases if requested.
				if opts.PruneReleases {
					keep := cfg.Release.Keep
					if keep <= 0 {
						keep = 5
					}
					pruneCmd := fmt.Sprintf(
						"ls -dt %s/v* 2>/dev/null | tail -n +%d | xargs -r rm -rf 2>/dev/null; true",
						releaseDir, keep+1,
					)
					_, _, _, _ = conn.Run(pruneCmd)
					PruneLocalSnapshots(localDir, keep)
				}
				break
			}
		}
	}

	rr.Elapsed = time.Since(start)
	return rr, nil
}

func execWith(d Dispatch) func(context.Context, cue.SSHConn, config.CueRef, config.Target) (cue.Result, error) {
	return func(ctx context.Context, conn cue.SSHConn, cr config.CueRef, target config.Target) (cue.Result, error) {
		switch cr.Nature {
		case "binary":
			return d.Binary.Execute(ctx, conn, cr, target)
		case "config":
			return d.Config.Execute(ctx, conn, cr, target)
		case "secret":
			return d.Secret.Execute(ctx, conn, cr, target)
		case "action":
			return d.Action.Execute(ctx, conn, cr, target)
		case "generate":
			return d.Generate.Execute(ctx, conn, cr, target)
		case "render":
			return d.Render.Execute(ctx, conn, cr, target)
		case "pack":
			return d.Pack.Execute(ctx, conn, cr, target)
		case "service":
			return d.Service.Execute(ctx, conn, cr, target)
		}
		return cue.Result{}, fmt.Errorf("unknown nature %q", cr.Nature)
	}
}

// resolvePostAction expands "restart:<svc>", "reload:<svc>", and "deploy:<svc>"
// shorthands into the actual service manager command by scanning service cues
// across all scenarios. The sudo flag ORs pa.Sudo with the service cue's Sudo.
// Returns the raw command and pa.Sudo unchanged when no matching cue is found.
func resolvePostAction(pa cue.PostAction, cfg *config.Config, tgt config.Target) (string, bool) {
	for _, prefix := range []string{"restart:", "reload:", "deploy:"} {
		if !strings.HasPrefix(pa.Cmd, prefix) {
			continue
		}
		svcName := strings.TrimPrefix(pa.Cmd, prefix)
		cr, ok := findServiceCue(cfg, svcName)
		if !ok {
			continue
		}
		action := strings.TrimSuffix(prefix, ":")
		cmds := manager.ExpandCommands(cr, tgt)
		if expanded, ok := cmds[action]; ok {
			return expanded, pa.Sudo || cr.Sudo
		}
	}
	return pa.Cmd, pa.Sudo
}

// findServiceCue returns the first service cue (nature: service) whose Name
// matches svcName, scanning all scenarios in cfg.
func findServiceCue(cfg *config.Config, svcName string) (config.CueRef, bool) {
	for _, sc := range cfg.Scenarios {
		for _, cr := range sc.Cues {
			if cr.ScenarioRef != "" {
				continue
			}
			if cr.Nature == "service" && cr.Name == svcName {
				return cr, true
			}
		}
	}
	return config.CueRef{}, false
}

// expandScenarioRef inlines the cues of a referenced scenario at the call-site position.
// NarrowCue/CueNames filter to a subset of the referenced scenario's cues.
// Nested refs are resolved recursively; depth guards against cycles.
//
// ownerScenario is the top-level topo-sort scenario that contains this ref —
// it is used as OnErrorScenario so that the parent's on_error: applies when
// an inlined cue fails, regardless of which referenced scenario it came from.
// ScenarioName/ScenarioDesc still carry the referenced scenario for display grouping.
func expandScenarioRef(ref config.CueRef, cfg *config.Config, baseEnv map[string]string, natureFilter []string, ownerScenario string, depth int) []Step {
	if depth > 10 {
		return nil // cycle guard
	}
	sc, ok := cfg.Scenarios[ref.ScenarioRef]
	if !ok {
		// Unreachable after config.Validate — scenario ref existence is checked at load time.
		return nil
	}
	var steps []Step
	for _, cr := range sc.Cues {
		if cr.ScenarioRef != "" {
			// Nested ref: owner stays the same top-level scenario.
			steps = append(steps, expandScenarioRef(cr, cfg, baseEnv, natureFilter, ownerScenario, depth+1)...)
			continue
		}
		// NarrowCue: narrow to a single named cue within the ref.
		if ref.NarrowCue != "" && cr.Name != ref.NarrowCue {
			continue
		}
		// CueNames: narrow to an explicit subset.
		if len(ref.CueNames) > 0 {
			found := false
			for _, name := range ref.CueNames {
				if name == cr.Name {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		if len(natureFilter) > 0 && !natureMatches(natureFilter, cr.Nature) {
			continue
		}
		cr.Env = mergeEnv(baseEnv, cfg.Defaults.Env, sc.Env, cr.Env)
		steps = append(steps, Step{
			Name:            cr.Name,
			ScenarioName:    ref.ScenarioRef, // display: show referenced scenario
			ScenarioDesc:    sc.Describe,
			OnErrorScenario: ownerScenario, // error handling: use composing parent
			CueRef:          cr,
			IsLocal:         cr.Local || cr.Nature == "generate",
		})
	}
	return steps
}

// mergeEnv merges env layers left to right; later layers override earlier ones.
// Returns nil if all layers are empty.
// natureMatches reports whether nature appears in the filter list.
func natureMatches(filter []string, nature string) bool {
	for _, f := range filter {
		if f == nature {
			return true
		}
	}
	return false
}

func mergeEnv(layers ...map[string]string) map[string]string {
	var total int
	for _, l := range layers {
		total += len(l)
	}
	if total == 0 {
		return nil
	}
	out := make(map[string]string, total)
	for _, l := range layers {
		for k, v := range l {
			out[k] = v
		}
	}
	return out
}

// runPrePost executes one pre/post entry.
// local:true → runs on the local machine via sh -c; otherwise runs over SSH.
func runPrePost(ctx context.Context, pp config.PrePost, conn interface {
	Run(string) (string, string, int, error)
}) error {
	if pp.Local {
		cmd := exec.CommandContext(ctx, "sh", "-c", pp.Cmd)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s failed: %w (output: %s)", pp.Cmd, err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	_, _, code, err := conn.Run(pp.Cmd)
	if err != nil {
		return fmt.Errorf("%s failed: %w", pp.Cmd, err)
	}
	if code != 0 {
		return fmt.Errorf("%s failed (exit %d)", pp.Cmd, code)
	}
	return nil
}

func tallyCounts(rr *RunResult, results []cue.Result) {
	for _, r := range results {
		switch r.Status {
		case cue.StatusChanged:
			rr.Changed++
		case cue.StatusEqual:
			rr.Equal++
		case cue.StatusFailed:
			rr.Failed++
		}
	}
}
