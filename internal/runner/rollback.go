// internal/runner/rollback.go
package runner

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
)

// RollbackOutcome records what happened during an automatic rollback triggered by on_error: rollback.
type RollbackOutcome struct {
	RestoredRelease string       // release ID whose snapshot was re-uploaded
	Results         []cue.Result // outcomes of rollback: action cues
	Err             error
}

// executeRollback restores the previous release and runs per-cue compensations + scenario rollback: block.
//
// Steps:
//  1. Locate the most recent local release snapshot (previous, not current).
//  2. Per-cue compensations in reverse execution order for cues where rollback: is enabled
//     (rollback: defer cues are skipped here):
//     - File natures (binary/config/secret/render/pack): re-upload from previous snapshot's CueArtifacts.
//     - Action natures: run the rollback.shell command.
//  2b. Deferred cues (rollback: defer): re-execute cue shell in original forward order,
//     after all file restores from step 2 complete.
//  3. If no cues had rollback: enabled (legacy/explicit on_error: rollback): fall back to
//     re-uploading all artifacts from the previous snapshot.
//  4. Run scenario-level rollback: action cues from each scenario in topo order.
func executeRollback(
	ctx context.Context,
	conn cue.SSHConn,
	cfg *config.Config,
	order []string,
	releaseID string, // ID of the failed deploy — available as $RELEASE_ID in rollback actions
	localDir string,
	target config.Target,
	dispatch Dispatch,
	onResult func(cue.Result),
	deployResults []cue.Result,
	deploySteps []Step,
) *RollbackOutcome {
	out := &RollbackOutcome{}

	// Step 1: find previous local release.
	prevID, err := findLatestLocalRelease(localDir)
	if err != nil {
		out.Err = fmt.Errorf("rollback: find previous release: %w", err)
		return out
	}
	out.RestoredRelease = prevID

	// Load previous manifest to resolve CueArtifacts for per-cue file restoration.
	prevManifest, _ := readLocalManifest(localDir, prevID)

	// Step 2: per-cue compensations in reverse execution order.
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
		if cr.Rollback == nil || !cr.Rollback.Enabled {
			continue
		}
		cuePairs = append(cuePairs, cuePair{step: step, result: result})
	}

	perCueRollback := len(cuePairs) > 0

	// Partition: regular compensations (reverse order) vs deferred re-runs (forward, after restores).
	var regularPairs []cuePair
	var deferredPairs []cuePair
	for _, p := range cuePairs {
		if p.step.CueRef.Rollback.Defer {
			deferredPairs = append(deferredPairs, p)
		} else {
			regularPairs = append(regularPairs, p)
		}
	}

	// Step 2: regular compensations in reverse execution order.
	for i := len(regularPairs) - 1; i >= 0; i-- {
		cr := regularPairs[i].step.CueRef
		switch cr.Nature {
		case "binary", "config", "secret", "render", "pack":
			// File restore: re-upload previous snapshot files for this specific cue.
			if prevManifest != nil {
				if cueFiles, ok := prevManifest.CueArtifacts[cr.Name]; ok {
					if err := reuploadCueArtifacts(conn, localDir, prevID, cueFiles, target); err != nil {
						out.Err = fmt.Errorf("rollback: restore cue %q: %w", cr.Name, err)
						return out
					}
				}
				// else: no previous snapshot for this cue (first-ever deploy) — nothing to restore
			}
		case "action", "service":
			if cr.Rollback.Shell != "" {
				sudo := cr.Rollback.Sudo || cr.Sudo || target.Sudo
				var runErr error
				if sudo {
					_, _, _, runErr = conn.RunSudo(cr.Rollback.Shell)
				} else {
					_, _, _, runErr = conn.Run(cr.Rollback.Shell)
				}
				if runErr != nil {
					out.Err = fmt.Errorf("rollback: cue %q compensation: %w", cr.Name, runErr)
					return out
				}
			}
		}
	}

	// Step 2b: deferred cues re-run in original forward order, after file restores.
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
			out.Err = fmt.Errorf("rollback: deferred %q: %w", cr.Name, runErr)
			return out
		}
	}

	// Step 3: fallback — if no per-cue rollback configured, restore all artifacts.
	if !perCueRollback {
		if err := reuploadSnapshotArtifacts(conn, localDir, prevID, target); err != nil {
			out.Err = fmt.Errorf("rollback: restore %s: %w", prevID, err)
			return out
		}
	}

	// Step 4: run scenario-level rollback: action cues.
	// RELEASE_ID is the *failed* deploy ID so scripts can reference the matching pre-deploy backup.
	baseEnv := map[string]string{"RELEASE_ID": releaseID}
	var rollbackSteps []Step
	for _, scName := range order {
		sc := cfg.Scenarios[scName]
		for _, cr := range sc.Rollback {
			if cr.ScenarioRef != "" {
				continue
			}
			cr.Nature = "action" // rollback entries are always remote actions
			cr.Env = mergeEnv(baseEnv, cfg.Defaults.Env, sc.Env, cr.Env)
			rollbackSteps = append(rollbackSteps, Step{
				Name:            cr.Name,
				ScenarioName:    scName,
				ScenarioDesc:    sc.Describe,
				OnErrorScenario: scName,
				CueRef:          cr,
			})
		}
	}
	if len(rollbackSteps) > 0 {
		results, err := executeSequential(ctx, rollbackSteps, conn, target, execWith(dispatch), onResult)
		out.Results = results
		if err != nil {
			out.Err = fmt.Errorf("rollback action failed: %w", err)
		}
	}
	return out
}

// effectiveOnError returns the on_error policy for a failing scenario.
// Priority: scenario.on_error → inferred from per-cue rollback: fields → cfg.Run.OnError → "halt".
func effectiveOnError(cfg *config.Config, failingScenario string) string {
	if sc, ok := cfg.Scenarios[failingScenario]; ok {
		if sc.OnError != "" {
			return sc.OnError
		}
		// Infer rollback if any direct cue has rollback: enabled.
		for _, cr := range sc.Cues {
			if cr.ScenarioRef == "" && cr.Rollback != nil && cr.Rollback.Enabled {
				return "rollback"
			}
		}
	}
	if cfg.Run.OnError != "" {
		return cfg.Run.OnError
	}
	return "halt"
}

// failingScenarioName returns the OnErrorScenario of the first StatusFailed result.
// For inline-expanded cues this is the composing parent scenario, not the referenced one,
// so the parent's on_error: policy applies when an inlined cue fails.
func failingScenarioName(results []cue.Result, steps []Step) string {
	for i, r := range results {
		if r.Status == cue.StatusFailed && i < len(steps) {
			return steps[i].OnErrorScenario
		}
	}
	return ""
}

// findLatestLocalRelease returns the name of the most recent vYYYYMMDD-* directory in localDir.
func findLatestLocalRelease(localDir string) (string, error) {
	entries, err := os.ReadDir(localDir)
	if err != nil {
		return "", err
	}
	var releases []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "v") {
			releases = append(releases, e.Name())
		}
	}
	if len(releases) == 0 {
		return "", fmt.Errorf("no releases found in %s", localDir)
	}
	sort.Strings(releases)
	return releases[len(releases)-1], nil
}

// readLocalManifest loads and parses the .regis-release file from localDir/releaseID/.
func readLocalManifest(localDir, releaseID string) (*ReleaseManifest, error) {
	data, err := os.ReadFile(filepath.Join(localDir, releaseID, ".regis-release"))
	if err != nil {
		return nil, err
	}
	var m ReleaseManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// reuploadCueArtifacts re-uploads files for a single cue from localDir/releaseID/
// using the provided snapshotKey → remotePath map.
func reuploadCueArtifacts(conn cue.SSHConn, localDir, releaseID string, cueFiles map[string]string, target config.Target) error {
	snapshotDir := filepath.Join(localDir, releaseID)
	for key, remotePath := range cueFiles {
		localFile := filepath.Join(snapshotDir, filepath.FromSlash(key))
		localData, err := os.ReadFile(localFile)
		if err != nil {
			continue // non-fatal: snapshot file may be absent (first-ever deploy)
		}
		if err := conn.UploadBytes(localData, remotePath, fs.FileMode(0644), target.Sudo); err != nil {
			return fmt.Errorf("upload %s → %s: %w", key, remotePath, err)
		}
	}
	return nil
}

// reuploadSnapshotArtifacts re-uploads every artifact file from localDir/releaseID/
// to its recorded remote path using the release manifest's Artifacts map.
// Used as a fallback when no per-cue rollback: fields are declared.
func reuploadSnapshotArtifacts(conn cue.SSHConn, localDir, releaseID string, target config.Target) error {
	manifestPath := filepath.Join(localDir, releaseID, ".regis-release")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest %s: %w", manifestPath, err)
	}
	var manifest ReleaseManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}

	snapshotDir := filepath.Join(localDir, releaseID)
	for key, remotePath := range manifest.Artifacts {
		localFile := filepath.Join(snapshotDir, filepath.FromSlash(key))
		localData, err := os.ReadFile(localFile)
		if err != nil {
			continue // non-fatal: snapshot file may be absent for some artifact types
		}
		if err := conn.UploadBytes(localData, remotePath, fs.FileMode(0644), target.Sudo); err != nil {
			return fmt.Errorf("upload %s → %s: %w", key, remotePath, err)
		}
	}
	return nil
}
