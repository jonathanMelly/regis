// internal/cue/context.go
package cue

import (
	"context"
	"io"
	"time"
)

type manifestKey struct{}

// Manifest holds state manifest data used during rdiff drift detection.
type Manifest struct {
	ID         string
	DeployedAt string // formatted for display
	DeployedBy string
	Hashes     map[string]string
}

// WithManifest returns a context carrying the state manifest for drift detection.
func WithManifest(ctx context.Context, m *Manifest) context.Context {
	return context.WithValue(ctx, manifestKey{}, m)
}

// ManifestFrom returns the manifest stored in ctx, or nil if absent.
func ManifestFrom(ctx context.Context) *Manifest {
	m, _ := ctx.Value(manifestKey{}).(*Manifest)
	return m
}

type debugWriterKey struct{}

// WithDebugWriter stores a writer that pipeline uses to print per-step debug headers.
// When set, the remote pre-check also runs sequentially so output is readable.
func WithDebugWriter(ctx context.Context, w io.Writer) context.Context {
	return context.WithValue(ctx, debugWriterKey{}, w)
}

// DebugWriterFrom returns the debug writer stored in ctx, or nil if absent.
func DebugWriterFrom(ctx context.Context) io.Writer {
	w, _ := ctx.Value(debugWriterKey{}).(io.Writer)
	return w
}

// RemoteStat holds the pre-fetched state of a remote file.
// Hash is populated only when mtime/size differ from the local file — ready for manifest.
type RemoteStat struct {
	Mtime time.Time
	Size  int64  // -1 when stat failed or file is absent
	Hash  string // empty when mtime+size matched (no hash needed)
}

type remoteStatsKey struct{}

// WithRemoteStats stores a bulk-prefetched map of remote file stats.
// Each key is a fully-resolved remote path.
func WithRemoteStats(ctx context.Context, stats map[string]RemoteStat) context.Context {
	return context.WithValue(ctx, remoteStatsKey{}, stats)
}

// RemoteStatsFrom returns the map stored in ctx, or nil if absent.
func RemoteStatsFrom(ctx context.Context) map[string]RemoteStat {
	m, _ := ctx.Value(remoteStatsKey{}).(map[string]RemoteStat)
	return m
}

type remoteFilesKey struct{}

// WithRemoteFiles stores the set of files known to exist on the remote target.
// Executors use this to skip Download/MD5 calls for absent files — treating them
// as new (StatusChanged) without a round-trip.
func WithRemoteFiles(ctx context.Context, paths []string) context.Context {
	set := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		if p != "" {
			set[p] = struct{}{}
		}
	}
	return context.WithValue(ctx, remoteFilesKey{}, set)
}

// RemoteFilesKnown reports whether a remote file set has been loaded into ctx.
func RemoteFilesKnown(ctx context.Context) bool {
	_, ok := ctx.Value(remoteFilesKey{}).(map[string]struct{})
	return ok
}

// RemoteFileExists reports whether path is present in the remote file set.
// Returns true if no set is loaded — callers must not skip the download.
func RemoteFileExists(ctx context.Context, path string) bool {
	set, ok := ctx.Value(remoteFilesKey{}).(map[string]struct{})
	if !ok {
		return true
	}
	_, exists := set[path]
	return exists
}

// SupplementRemoteFiles explicitly stats each path in paths and adds the found
// ones to the existing remote file set. Paths already in the set are skipped.
// Returns ctx unchanged when no set is loaded or paths is empty.
// Use this to extend coverage for absolute-path dests that lie outside the
// working directory scanned by the initial find (e.g. /etc/nginx/…).
func SupplementRemoteFiles(ctx context.Context, conn SSHConn, paths []string) context.Context {
	set, ok := ctx.Value(remoteFilesKey{}).(map[string]struct{})
	if !ok || len(paths) == 0 || conn == nil {
		return ctx
	}
	var unknown []string
	for _, p := range paths {
		if _, exists := set[p]; !exists {
			unknown = append(unknown, p)
		}
	}
	if len(unknown) == 0 {
		return ctx
	}
	found := BulkStatRemote(conn, unknown)
	newSet := make(map[string]struct{}, len(set)+len(found))
	for k := range set {
		newSet[k] = struct{}{}
	}
	for p := range found {
		newSet[p] = struct{}{}
	}
	return context.WithValue(ctx, remoteFilesKey{}, newSet)
}

type preStepKey struct{}

// WithPreStep stores a callback invoked by the pipeline just before each step executes.
// Use it to drive progress indicators with the current scenario / cue context.
// The callback receives (scenarioName, cueName, scenarioDesc).
func WithPreStep(ctx context.Context, fn func(scenario, cue, desc string)) context.Context {
	return context.WithValue(ctx, preStepKey{}, fn)
}

// PreStepFrom returns the pre-step callback stored in ctx, or nil if absent.
func PreStepFrom(ctx context.Context) func(scenario, cue, desc string) {
	fn, _ := ctx.Value(preStepKey{}).(func(string, string, string))
	return fn
}

// StepInfo carries identifying details about one cue step.
// Used by WithPrePhase to announce all upcoming steps before a parallel check begins.
type StepInfo struct {
	Name              string
	ScenarioName      string
	ScenarioDesc      string
	GroupScenarioName string // top-level scenario for display grouping
	GroupScenarioDesc string
}

type prePhaseKey struct{}

// WithPrePhase stores a callback invoked by the pipeline immediately before a parallel
// check phase begins, with info for every step in the phase. Fires once, before any
// goroutine starts, so callers can print all expected cue names in one batch — avoiding
// interleaved start/result lines.
func WithPrePhase(ctx context.Context, fn func(steps []StepInfo)) context.Context {
	return context.WithValue(ctx, prePhaseKey{}, fn)
}

// PrePhaseFrom returns the pre-phase callback stored in ctx, or nil if absent.
func PrePhaseFrom(ctx context.Context) func(steps []StepInfo) {
	fn, _ := ctx.Value(prePhaseKey{}).(func([]StepInfo))
	return fn
}

type checkResultKey struct{}

// WithCheckResult stores a callback invoked by the pipeline immediately after each
// parallel cue check completes, from within the goroutine. Use it for live per-cue output.
func WithCheckResult(ctx context.Context, fn func(Result)) context.Context {
	return context.WithValue(ctx, checkResultKey{}, fn)
}

// CheckResultFrom returns the check-result callback stored in ctx, or nil if absent.
func CheckResultFrom(ctx context.Context) func(Result) {
	fn, _ := ctx.Value(checkResultKey{}).(func(Result))
	return fn
}

type cueProgressKey struct{}

// WithCueProgress stores a callback invoked by the pipeline after each parallel cue check
// completes. checked is the running total; total is the number of cues in the phase.
// Use it to drive a "N/M cues" fallback indicator when no file-progress is active.
func WithCueProgress(ctx context.Context, fn func(checked, total int)) context.Context {
	return context.WithValue(ctx, cueProgressKey{}, fn)
}

// CueProgressFrom returns the cue-progress callback stored in ctx, or nil if absent.
func CueProgressFrom(ctx context.Context) func(checked, total int) {
	fn, _ := ctx.Value(cueProgressKey{}).(func(int, int))
	return fn
}

type localDirKey struct{}

// WithLocalDir stores the local project directory (cfg.BaseDir) so local shell
// executors (action local, generate, render) run with the project root as CWD.
func WithLocalDir(ctx context.Context, dir string) context.Context {
	if dir == "" {
		return ctx
	}
	return context.WithValue(ctx, localDirKey{}, dir)
}

// LocalDirFrom returns the local project directory stored in ctx, or "" if absent.
func LocalDirFrom(ctx context.Context) string {
	s, _ := ctx.Value(localDirKey{}).(string)
	return s
}

type fileProgressKey struct{}

// WithFileProgress stores a callback that multi-file executors (pack, config multi-src,
// render) invoke after each file is processed. cueName identifies the cue; scanned is
// the running count; total is the full file count for this cue.
// Safe for concurrent use: Spinner.Update already holds its own mutex.
func WithFileProgress(ctx context.Context, fn func(cueName string, scanned, total int)) context.Context {
	return context.WithValue(ctx, fileProgressKey{}, fn)
}

// FileProgressFrom returns the file-progress callback stored in ctx, or nil if absent.
func FileProgressFrom(ctx context.Context) func(cueName string, scanned, total int) {
	fn, _ := ctx.Value(fileProgressKey{}).(func(string, int, int))
	return fn
}

type applyStepKey struct{}

// WithApplyStep stores a callback invoked by the pipeline just before each
// apply step executes in stage 2 of the remote pipeline (not the pre-check).
// Use it to drive a "currently applying" indicator in the TUI.
func WithApplyStep(ctx context.Context, fn func(scenario, cue string)) context.Context {
	return context.WithValue(ctx, applyStepKey{}, fn)
}

// ApplyStepFrom returns the apply-step callback stored in ctx, or nil if absent.
func ApplyStepFrom(ctx context.Context) func(scenario, cue string) {
	fn, _ := ctx.Value(applyStepKey{}).(func(string, string))
	return fn
}

type applyResultKey struct{}

// WithApplyResult stores a callback invoked by the pipeline after each step
// in stage 2 of the remote pipeline completes (both applied and equal-skipped steps).
func WithApplyResult(ctx context.Context, fn func(Result)) context.Context {
	return context.WithValue(ctx, applyResultKey{}, fn)
}

// ApplyResultFrom returns the apply-result callback stored in ctx, or nil if absent.
func ApplyResultFrom(ctx context.Context) func(Result) {
	fn, _ := ctx.Value(applyResultKey{}).(func(Result))
	return fn
}
