// internal/cue/generate.go
// doc:nature generate
// Runs a shell command locally to produce artifacts (e.g. rendered config files).
// Always executes — even during rdiff — so downstream config cues compare fresh output.
// Reports = by default (no noise in rdiff output); use changed_when: true to mark as changed.
// rollback: not applicable — generate cues run locally and produce no remote state to restore.
package cue

import (
	"context"
	"fmt"
	"strings"
	"time"

	"git.disroot.org/jmy/regis/internal/config"
)

// GenerateExecutor handles nature: generate cues.
// It always runs its shell command on the local machine, in every mode including rdiff.
// Default status is StatusEqual so generator cues produce no noise in rdiff output.
type GenerateExecutor struct{}

// NewGenerateExecutor creates a GenerateExecutor.
func NewGenerateExecutor() *GenerateExecutor { return &GenerateExecutor{} }

// Execute runs the generator shell command locally and returns StatusEqual by default.
// It always runs, even when the context carries the dry-run flag, so that config cues
// that follow it can compare against freshly produced output.
func (e *GenerateExecutor) Execute(ctx context.Context, _ SSHConn, cr config.CueRef, _ config.Target) (Result, error) {
	start := time.Now()
	r := Result{
		CueName: cr.Name,
		Nature:  "generate",
		IsLocal: true,
	}

	stdout, stderr, exitCode, runErr := runLocal(ctx, cr.Shell, cr.Env)
	if runErr != nil {
		r.Status = StatusFailed
		r.Err = runErr
		r.Stdout = stdout
		r.Stderr = stderr
		r.Elapsed = time.Since(start)
		return r, nil
	}

	r.Stdout = stdout
	r.Stderr = stderr

	// Evaluate failed_when
	failed, err := evalWhen(ctx, cr.FailedWhen, stdout, stderr, exitCode)
	if err != nil {
		r.Status = StatusFailed
		r.Err = fmt.Errorf("failed_when eval: %w", err)
		r.Elapsed = time.Since(start)
		return r, nil
	}
	if failed {
		r.Status = StatusFailed
		r.Err = fmt.Errorf("exit %d: %s", exitCode, strings.TrimSpace(stderr))
		r.Elapsed = time.Since(start)
		return r, nil
	}

	// Default: non-zero exit = failed (when no failed_when expression)
	if exitCode != 0 && cr.FailedWhen == (config.WhenExpr{}) {
		r.Status = StatusFailed
		r.Err = fmt.Errorf("exit %d: %s", exitCode, strings.TrimSpace(stderr))
		r.Elapsed = time.Since(start)
		return r, nil
	}

	// Evaluate changed_when — default is StatusEqual (unlike action which defaults to changed)
	changed, err := evalWhen(ctx, cr.ChangedWhen, stdout, stderr, exitCode)
	if err != nil {
		r.Status = StatusFailed
		r.Err = fmt.Errorf("changed_when eval: %w", err)
		r.Elapsed = time.Since(start)
		return r, nil
	}

	if changed {
		r.Status = StatusChanged
	} else {
		r.Status = StatusEqual
	}
	r.Elapsed = time.Since(start)
	return r, nil
}
