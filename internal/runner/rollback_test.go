// internal/runner/rollback_test.go
package runner

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
)

func TestFindLatestLocalRelease_noDir(t *testing.T) {
	_, err := findLatestLocalRelease(t.TempDir() + "/nonexistent")
	if err == nil {
		t.Error("expected error for missing dir")
	}
}

func TestFindLatestLocalRelease_empty(t *testing.T) {
	_, err := findLatestLocalRelease(t.TempDir())
	if err == nil {
		t.Error("expected error when no releases exist")
	}
}

func TestFindLatestLocalRelease_sortOrder(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"v20260601-120000", "v20260606-143021", "v20260603-080000"} {
		os.Mkdir(filepath.Join(dir, name), 0755)
	}
	got, err := findLatestLocalRelease(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "v20260606-143021" {
		t.Errorf("expected newest, got %q", got)
	}
}

func TestEffectiveOnError_scenarioOverride(t *testing.T) {
	cfg := &config.Config{
		Scenarios: map[string]config.Scenario{
			"Deploy": {OnError: "rollback"},
		},
		Run: config.RunConfig{OnError: "halt"},
	}
	if got := effectiveOnError(cfg, "Deploy"); got != "rollback" {
		t.Errorf("expected rollback, got %q", got)
	}
}

func TestEffectiveOnError_globalFallback(t *testing.T) {
	cfg := &config.Config{
		Scenarios: map[string]config.Scenario{
			"Setup": {},
		},
		Run: config.RunConfig{OnError: "continue"},
	}
	if got := effectiveOnError(cfg, "Setup"); got != "continue" {
		t.Errorf("expected continue, got %q", got)
	}
}

func TestEffectiveOnError_defaultHalt(t *testing.T) {
	cfg := &config.Config{
		Scenarios: map[string]config.Scenario{
			"Setup": {},
		},
	}
	if got := effectiveOnError(cfg, "Setup"); got != "halt" {
		t.Errorf("expected halt, got %q", got)
	}
}

func TestEffectiveOnError_unknownScenario(t *testing.T) {
	cfg := &config.Config{}
	if got := effectiveOnError(cfg, ""); got != "halt" {
		t.Errorf("expected halt for unknown scenario, got %q", got)
	}
}

func TestFailingScenarioName(t *testing.T) {
	results := []cue.Result{
		{Status: cue.StatusEqual},
		{Status: cue.StatusFailed},
	}
	steps := []Step{
		{OnErrorScenario: "Setup"},
		{OnErrorScenario: "Deploy"},
	}
	if got := failingScenarioName(results, steps); got != "Deploy" {
		t.Errorf("expected Deploy, got %q", got)
	}
}

func TestFailingScenarioName_noFailure(t *testing.T) {
	results := []cue.Result{{Status: cue.StatusChanged}}
	steps := []Step{{OnErrorScenario: "Deploy"}}
	if got := failingScenarioName(results, steps); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// TestFailingScenarioName_inlineRef verifies that when an inline-expanded cue fails,
// it's the composing parent's on_error that applies — not the referenced scenario's.
func TestFailingScenarioName_inlineRef(t *testing.T) {
	results := []cue.Result{
		{Status: cue.StatusFailed},
	}
	// cue came from scenario "app" (ScenarioName) but is owned by "deploy" (OnErrorScenario)
	steps := []Step{
		{ScenarioName: "app", OnErrorScenario: "deploy"},
	}
	if got := failingScenarioName(results, steps); got != "deploy" {
		t.Errorf("expected deploy (parent), got %q", got)
	}
}

func TestReuploadSnapshotArtifacts_noManifest(t *testing.T) {
	err := reuploadSnapshotArtifacts(nil, t.TempDir(), "v20260606-143021", config.Target{})
	if err == nil {
		t.Error("expected error when manifest missing")
	}
}

func TestReuploadSnapshotArtifacts_uploadsArtifacts(t *testing.T) {
	dir := t.TempDir()
	releaseID := "v20260606-143021"
	releaseDir := filepath.Join(dir, releaseID)
	os.MkdirAll(releaseDir, 0755)

	// Write a fake manifest with one artifact
	manifest := "release: " + releaseID + "\nartifacts:\n  mybin: /opt/app/mybin\n"
	os.WriteFile(filepath.Join(releaseDir, ".regis-release"), []byte(manifest), 0644)
	// Write the snapshot file
	os.WriteFile(filepath.Join(releaseDir, "mybin"), []byte("binary data"), 0644)

	var uploadedTo string
	var uploadedData []byte
	conn := &mockSSHConn{
		uploadFn: func(data []byte, remote string) {
			uploadedTo = remote
			uploadedData = data
		},
	}

	err := reuploadSnapshotArtifacts(conn, dir, releaseID, config.Target{})
	if err != nil {
		t.Fatal(err)
	}
	if uploadedTo != "/opt/app/mybin" {
		t.Errorf("expected upload to /opt/app/mybin, got %q", uploadedTo)
	}
	if string(uploadedData) != "binary data" {
		t.Errorf("unexpected upload content: %q", uploadedData)
	}
}

// funcExecutor is a minimal cue.Executor that calls fn.
type funcExecutor struct {
	fn func(config.CueRef) cue.Result
}

func (e *funcExecutor) Execute(_ context.Context, _ cue.SSHConn, cr config.CueRef, _ config.Target) (cue.Result, error) {
	return e.fn(cr), nil
}

var _ cue.Executor = (*funcExecutor)(nil)

// mockSSHConn is a minimal cue.SSHConn for rollback tests.
type mockSSHConn struct {
	uploadFn   func(data []byte, remote string)
	runFn      func(cmd string)
	runSudoFn  func(cmd string)
	uploadErr  error // when non-nil, UploadBytes returns this error
	runErr     error // when non-nil, Run returns this error
}

func (m *mockSSHConn) Upload(l, r string, mode fs.FileMode, sudo bool) error { return nil }
func (m *mockSSHConn) UploadBytes(data []byte, remote string, mode fs.FileMode, sudo bool) error {
	if m.uploadFn != nil {
		m.uploadFn(data, remote)
	}
	return m.uploadErr
}
func (m *mockSSHConn) Run(cmd string) (string, string, int, error) {
	if m.runFn != nil {
		m.runFn(cmd)
	}
	if m.runErr != nil {
		return "", "", 1, m.runErr
	}
	return "", "", 0, nil
}
func (m *mockSSHConn) RunSudo(cmd string) (string, string, int, error) {
	if m.runSudoFn != nil {
		m.runSudoFn(cmd)
	}
	return "", "", 0, nil
}
func (m *mockSSHConn) RunWithEnv(cmd string, env map[string]string) (string, string, int, error) {
	return "", "", 0, nil
}
func (m *mockSSHConn) Download(path string) ([]byte, error) { return nil, nil }
func (m *mockSSHConn) Exists(path string) (bool, error)     { return false, nil }
func (m *mockSSHConn) PathSep() string                      { return "/" }

// Compile-time check.
var _ cue.SSHConn = (*mockSSHConn)(nil)

func TestEffectiveOnError_inferredFromCueRollback(t *testing.T) {
	cfg := &config.Config{
		Scenarios: map[string]config.Scenario{
			"Deploy": {
				Cues: []config.CueRef{
					{Name: "bin", Nature: "binary", Rollback: &config.CueRollback{Enabled: true}},
				},
			},
		},
	}
	if got := effectiveOnError(cfg, "Deploy"); got != "rollback" {
		t.Errorf("expected rollback inferred from cue, got %q", got)
	}
}

func TestEffectiveOnError_explicitOverridesInferred(t *testing.T) {
	cfg := &config.Config{
		Scenarios: map[string]config.Scenario{
			"Deploy": {
				OnError: "continue",
				Cues: []config.CueRef{
					{Name: "bin", Nature: "binary", Rollback: &config.CueRollback{Enabled: true}},
				},
			},
		},
	}
	// Explicit on_error: continue wins over inferred rollback.
	if got := effectiveOnError(cfg, "Deploy"); got != "continue" {
		t.Errorf("expected continue (explicit), got %q", got)
	}
}

func TestEffectiveOnError_disabledRollbackNotInferred(t *testing.T) {
	cfg := &config.Config{
		Scenarios: map[string]config.Scenario{
			"Deploy": {
				Cues: []config.CueRef{
					{Name: "bin", Nature: "binary", Rollback: &config.CueRollback{Enabled: false}},
				},
			},
		},
	}
	// rollback: false — not inferred.
	if got := effectiveOnError(cfg, "Deploy"); got != "halt" {
		t.Errorf("expected halt (disabled rollback not inferred), got %q", got)
	}
}

func TestReadLocalManifest_roundtrip(t *testing.T) {
	dir := t.TempDir()
	releaseID := "v20260607-120000"
	releaseDir := filepath.Join(dir, releaseID)
	os.MkdirAll(releaseDir, 0755)

	m := ReleaseManifest{
		Release:   releaseID,
		Scenarios: []string{"app"},
		CueArtifacts: map[string]map[string]string{
			"bin": {"bin": "/opt/app/bin"},
		},
	}
	data, _ := yaml.Marshal(m)
	os.WriteFile(filepath.Join(releaseDir, ".regis-release"), data, 0644)

	got, err := readLocalManifest(dir, releaseID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CueArtifacts["bin"]["bin"] != "/opt/app/bin" {
		t.Errorf("CueArtifacts not preserved: %v", got.CueArtifacts)
	}
}

func TestReuploadCueArtifacts_uploadsCorrectFile(t *testing.T) {
	dir := t.TempDir()
	releaseID := "v20260607-120000"
	snapshotDir := filepath.Join(dir, releaseID)
	os.MkdirAll(snapshotDir, 0755)
	os.WriteFile(filepath.Join(snapshotDir, "bin"), []byte("binary data"), 0644)

	var uploadedTo string
	var uploadedData []byte
	conn := &mockSSHConn{
		uploadFn: func(data []byte, remote string) {
			uploadedTo = remote
			uploadedData = data
		},
	}

	cueFiles := map[string]string{"bin": "/opt/app/bin"}
	err := reuploadCueArtifacts(conn, dir, releaseID, cueFiles, config.Target{})
	if err != nil {
		t.Fatal(err)
	}
	if uploadedTo != "/opt/app/bin" {
		t.Errorf("expected upload to /opt/app/bin, got %q", uploadedTo)
	}
	if string(uploadedData) != "binary data" {
		t.Errorf("unexpected data: %q", uploadedData)
	}
}

func TestReuploadCueArtifacts_missingFileIsNonfatal(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "v1"), 0755)

	conn := &mockSSHConn{}
	// File "bin" doesn't exist in snapshot — should not error.
	err := reuploadCueArtifacts(conn, dir, "v1", map[string]string{"bin": "/opt/bin"}, config.Target{})
	if err != nil {
		t.Errorf("missing snapshot file must be non-fatal, got: %v", err)
	}
}

// ── executeRollback ────────────────────────────────────────────────────────

// makeTestSnapshot writes a release snapshot directory with a manifest and files.
func makeTestSnapshot(t *testing.T, dir, releaseID string, m ReleaseManifest, files map[string][]byte) {
	t.Helper()
	d := filepath.Join(dir, releaseID)
	os.MkdirAll(d, 0755)
	data, _ := yaml.Marshal(m)
	os.WriteFile(filepath.Join(d, ".regis-release"), data, 0644)
	for name, content := range files {
		p := filepath.Join(d, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, content, 0644)
	}
}

func TestExecuteRollback_noPreviousRelease_returnsError(t *testing.T) {
	dir := t.TempDir() // empty — no releases
	out := executeRollback(context.Background(), &mockSSHConn{}, &config.Config{},
		nil, "v20260607-fail", dir, config.Target{}, Dispatch{}, nil, nil, nil)
	if out.Err == nil {
		t.Error("expected error when no previous release exists")
	}
}

func TestExecuteRollback_fileNature_restoresFromCueArtifacts(t *testing.T) {
	dir := t.TempDir()
	prevID := "v20260601-120000"
	makeTestSnapshot(t, dir, prevID, ReleaseManifest{
		Release: prevID,
		CueArtifacts: map[string]map[string]string{
			"bin": {"bin": "/opt/app/bin"},
		},
	}, map[string][]byte{"bin": []byte("previous binary")})

	var uploads []struct{ data []byte; remote string }
	conn := &mockSSHConn{
		uploadFn: func(data []byte, remote string) {
			uploads = append(uploads, struct{ data []byte; remote string }{data, remote})
		},
	}

	cfg := &config.Config{Scenarios: map[string]config.Scenario{"Deploy": {}}}
	steps := []Step{{CueRef: config.CueRef{Name: "bin", Nature: "binary",
		Rollback: &config.CueRollback{Enabled: true}}, OnErrorScenario: "Deploy"}}
	results := []cue.Result{{CueName: "bin", Status: cue.StatusChanged}}

	out := executeRollback(context.Background(), conn, cfg, []string{"Deploy"},
		"v20260607-fail", dir, config.Target{}, Dispatch{}, nil, results, steps)

	if out.Err != nil {
		t.Fatalf("unexpected error: %v", out.Err)
	}
	if len(uploads) != 1 || uploads[0].remote != "/opt/app/bin" {
		t.Errorf("expected upload to /opt/app/bin, got %v", uploads)
	}
	if string(uploads[0].data) != "previous binary" {
		t.Errorf("unexpected upload content: %q", uploads[0].data)
	}
}

func TestExecuteRollback_actionNature_runsShellCommand(t *testing.T) {
	dir := t.TempDir()
	prevID := "v20260601-120000"
	makeTestSnapshot(t, dir, prevID, ReleaseManifest{Release: prevID}, nil)

	var runCmds []string
	conn := &mockSSHConn{runFn: func(cmd string) { runCmds = append(runCmds, cmd) }}

	cfg := &config.Config{Scenarios: map[string]config.Scenario{"Deploy": {}}}
	steps := []Step{{CueRef: config.CueRef{Name: "go-offline", Nature: "action",
		Rollback: &config.CueRollback{Enabled: true, Shell: "rm -f maintenance.flag"}},
		OnErrorScenario: "Deploy"}}
	results := []cue.Result{{CueName: "go-offline", Status: cue.StatusChanged}}

	out := executeRollback(context.Background(), conn, cfg, []string{"Deploy"},
		"v20260607-fail", dir, config.Target{}, Dispatch{}, nil, results, steps)

	if out.Err != nil {
		t.Fatalf("unexpected error: %v", out.Err)
	}
	if len(runCmds) != 1 || runCmds[0] != "rm -f maintenance.flag" {
		t.Errorf("expected compensation command, got %v", runCmds)
	}
}

func TestExecuteRollback_actionNature_sudoUsesRunSudo(t *testing.T) {
	dir := t.TempDir()
	prevID := "v20260601-120000"
	makeTestSnapshot(t, dir, prevID, ReleaseManifest{Release: prevID}, nil)

	var sudoCmds []string
	conn := &mockSSHConn{runSudoFn: func(cmd string) { sudoCmds = append(sudoCmds, cmd) }}

	cfg := &config.Config{Scenarios: map[string]config.Scenario{"Deploy": {}}}
	steps := []Step{{CueRef: config.CueRef{Name: "stop-svc", Nature: "action",
		Rollback: &config.CueRollback{Enabled: true, Shell: "systemctl stop myapp", Sudo: true}},
		OnErrorScenario: "Deploy"}}
	results := []cue.Result{{CueName: "stop-svc", Status: cue.StatusChanged}}

	out := executeRollback(context.Background(), conn, cfg, []string{"Deploy"},
		"v20260607-fail", dir, config.Target{}, Dispatch{}, nil, results, steps)

	if out.Err != nil {
		t.Fatalf("unexpected error: %v", out.Err)
	}
	if len(sudoCmds) != 1 || sudoCmds[0] != "systemctl stop myapp" {
		t.Errorf("expected sudo command, got %v", sudoCmds)
	}
}

func TestExecuteRollback_reverseOrder(t *testing.T) {
	dir := t.TempDir()
	prevID := "v20260601-120000"
	makeTestSnapshot(t, dir, prevID, ReleaseManifest{Release: prevID}, nil)

	var runOrder []string
	conn := &mockSSHConn{runFn: func(cmd string) { runOrder = append(runOrder, cmd) }}

	cfg := &config.Config{Scenarios: map[string]config.Scenario{"Deploy": {}}}
	steps := []Step{
		{CueRef: config.CueRef{Name: "a", Nature: "action", Rollback: &config.CueRollback{Enabled: true, Shell: "undo-a"}}},
		{CueRef: config.CueRef{Name: "b", Nature: "action", Rollback: &config.CueRollback{Enabled: true, Shell: "undo-b"}}},
		{CueRef: config.CueRef{Name: "c", Nature: "action", Rollback: &config.CueRollback{Enabled: true, Shell: "undo-c"}}},
	}
	results := []cue.Result{
		{CueName: "a", Status: cue.StatusChanged},
		{CueName: "b", Status: cue.StatusChanged},
		{CueName: "c", Status: cue.StatusChanged},
	}

	out := executeRollback(context.Background(), conn, cfg, nil,
		"v20260607-fail", dir, config.Target{}, Dispatch{}, nil, results, steps)

	if out.Err != nil {
		t.Fatalf("unexpected error: %v", out.Err)
	}
	want := []string{"undo-c", "undo-b", "undo-a"}
	if len(runOrder) != 3 || runOrder[0] != want[0] || runOrder[1] != want[1] || runOrder[2] != want[2] {
		t.Errorf("expected reverse order %v, got %v", want, runOrder)
	}
}

func TestExecuteRollback_skippedCuesExcluded(t *testing.T) {
	dir := t.TempDir()
	prevID := "v20260601-120000"
	makeTestSnapshot(t, dir, prevID, ReleaseManifest{Release: prevID}, nil)

	var runCmds []string
	conn := &mockSSHConn{runFn: func(cmd string) { runCmds = append(runCmds, cmd) }}

	cfg := &config.Config{Scenarios: map[string]config.Scenario{"Deploy": {}}}
	steps := []Step{
		{CueRef: config.CueRef{Name: "skipped-cue", Nature: "action",
			Rollback: &config.CueRollback{Enabled: true, Shell: "should-not-run"}}},
		{CueRef: config.CueRef{Name: "ran-cue", Nature: "action",
			Rollback: &config.CueRollback{Enabled: true, Shell: "should-run"}}},
	}
	results := []cue.Result{
		{CueName: "skipped-cue", Status: cue.StatusSkipped},
		{CueName: "ran-cue", Status: cue.StatusChanged},
	}

	out := executeRollback(context.Background(), conn, cfg, nil,
		"v20260607-fail", dir, config.Target{}, Dispatch{}, nil, results, steps)

	if out.Err != nil {
		t.Fatalf("unexpected error: %v", out.Err)
	}
	if len(runCmds) != 1 || runCmds[0] != "should-run" {
		t.Errorf("expected only ran-cue compensated; got cmds: %v", runCmds)
	}
}

func TestExecuteRollback_fallback_noPerCueRollback(t *testing.T) {
	dir := t.TempDir()
	prevID := "v20260601-120000"
	// Previous manifest has Artifacts (old format) — no CueArtifacts.
	makeTestSnapshot(t, dir, prevID, ReleaseManifest{
		Release:   prevID,
		Artifacts: map[string]string{"bin": "/opt/app/bin"},
	}, map[string][]byte{"bin": []byte("fallback binary")})

	var uploads []string
	conn := &mockSSHConn{uploadFn: func(_ []byte, remote string) { uploads = append(uploads, remote) }}

	cfg := &config.Config{Scenarios: map[string]config.Scenario{"Deploy": {}}}
	// Deploy steps have NO rollback: field — should fall back to reuploadSnapshotArtifacts.
	steps := []Step{{CueRef: config.CueRef{Name: "bin", Nature: "binary"}, OnErrorScenario: "Deploy"}}
	results := []cue.Result{{CueName: "bin", Status: cue.StatusChanged}}

	out := executeRollback(context.Background(), conn, cfg, []string{"Deploy"},
		"v20260607-fail", dir, config.Target{}, Dispatch{}, nil, results, steps)

	if out.Err != nil {
		t.Fatalf("unexpected error: %v", out.Err)
	}
	// Fallback: all artifacts from Artifacts map re-uploaded.
	if len(uploads) != 1 || uploads[0] != "/opt/app/bin" {
		t.Errorf("expected fallback upload to /opt/app/bin, got %v", uploads)
	}
}

func TestExecuteRollback_actionError_stopsRollback(t *testing.T) {
	dir := t.TempDir()
	prevID := "v20260601-120000"
	makeTestSnapshot(t, dir, prevID, ReleaseManifest{Release: prevID}, nil)

	conn := &mockSSHConn{runErr: fmt.Errorf("connection lost")}

	cfg := &config.Config{Scenarios: map[string]config.Scenario{"Deploy": {}}}
	steps := []Step{{CueRef: config.CueRef{Name: "go-offline", Nature: "action",
		Rollback: &config.CueRollback{Enabled: true, Shell: "some-cmd"}},
		OnErrorScenario: "Deploy"}}
	results := []cue.Result{{CueName: "go-offline", Status: cue.StatusChanged}}

	out := executeRollback(context.Background(), conn, cfg, nil,
		"v20260607-fail", dir, config.Target{}, Dispatch{}, nil, results, steps)

	if out.Err == nil {
		t.Error("expected error when action compensation fails")
	}
}

func TestExecuteRollback_scenarioLevelRollback_runs(t *testing.T) {
	dir := t.TempDir()
	prevID := "v20260601-120000"
	makeTestSnapshot(t, dir, prevID, ReleaseManifest{Release: prevID}, nil)

	var scenarioRollbackCues []string
	exec := &funcExecutor{fn: func(cr config.CueRef) cue.Result {
		scenarioRollbackCues = append(scenarioRollbackCues, cr.Name)
		return cue.Result{CueName: cr.Name, Status: cue.StatusChanged}
	}}

	cfg := &config.Config{
		Scenarios: map[string]config.Scenario{
			"Deploy": {
				Rollback: []config.CueRef{
					{Name: "restore-db", Shell: "php artisan db:restore --label=pre-${RELEASE_ID}"},
				},
			},
		},
	}
	// No per-cue rollback — fallback path; then scenario block runs.
	steps := []Step{{CueRef: config.CueRef{Name: "bin", Nature: "binary"}, OnErrorScenario: "Deploy"}}
	results := []cue.Result{{CueName: "bin", Status: cue.StatusChanged}}

	out := executeRollback(context.Background(), &mockSSHConn{}, cfg, []string{"Deploy"},
		"v20260607-fail", dir, config.Target{}, Dispatch{Action: exec}, nil, results, steps)

	if out.Err != nil {
		t.Fatalf("unexpected error: %v", out.Err)
	}
	if len(scenarioRollbackCues) != 1 || scenarioRollbackCues[0] != "restore-db" {
		t.Errorf("expected scenario-level rollback cue to run, got %v", scenarioRollbackCues)
	}
}

// ── rollback: defer ────────────────────────────────────────────────────────────

// TestExecuteRollback_defer_runsAfterFileRestores verifies that a deferred cue's
// shell runs AFTER pack file restores, not before.
func TestExecuteRollback_defer_runsAfterFileRestores(t *testing.T) {
	dir := t.TempDir()
	prevID := "v20260601-120000"
	makeTestSnapshot(t, dir, prevID, ReleaseManifest{
		Release: prevID,
		CueArtifacts: map[string]map[string]string{
			"vendor": {"vendor/autoload.php": "/var/www/vendor/autoload.php"},
		},
	}, map[string][]byte{"vendor/autoload.php": []byte("<?php // prev")})

	var order []string
	conn := &mockSSHConn{
		uploadFn: func(_ []byte, remote string) { order = append(order, "upload:"+remote) },
		runFn:    func(cmd string) { order = append(order, "run:"+cmd) },
	}

	cfg := &config.Config{Scenarios: map[string]config.Scenario{"Deploy": {}}}
	steps := []Step{
		{CueRef: config.CueRef{Name: "vendor", Nature: "pack",
			Rollback: &config.CueRollback{Enabled: true}}},
		{CueRef: config.CueRef{Name: "install-deps", Nature: "action", Shell: "composer install",
			Rollback: &config.CueRollback{Enabled: true, Defer: true}}},
	}
	results := []cue.Result{
		{CueName: "vendor", Status: cue.StatusChanged},
		{CueName: "install-deps", Status: cue.StatusChanged},
	}

	out := executeRollback(context.Background(), conn, cfg, nil,
		"v20260607-fail", dir, config.Target{}, Dispatch{}, nil, results, steps)

	if out.Err != nil {
		t.Fatalf("unexpected error: %v", out.Err)
	}
	// File restore must happen before composer install.
	if len(order) != 2 {
		t.Fatalf("expected 2 operations, got %v", order)
	}
	if order[0] != "upload:/var/www/vendor/autoload.php" {
		t.Errorf("expected file restore first, got %q", order[0])
	}
	if order[1] != "run:composer install" {
		t.Errorf("expected deferred run second, got %q", order[1])
	}
}

// TestExecuteRollback_defer_notRunInReversePhase verifies the deferred cue is
// excluded from reverse-order compensation.
func TestExecuteRollback_defer_notRunInReversePhase(t *testing.T) {
	dir := t.TempDir()
	prevID := "v20260601-120000"
	makeTestSnapshot(t, dir, prevID, ReleaseManifest{Release: prevID}, nil)

	var runCmds []string
	conn := &mockSSHConn{runFn: func(cmd string) { runCmds = append(runCmds, cmd) }}

	cfg := &config.Config{Scenarios: map[string]config.Scenario{"Deploy": {}}}
	steps := []Step{
		{CueRef: config.CueRef{Name: "a", Nature: "action",
			Rollback: &config.CueRollback{Enabled: true, Shell: "undo-a"}}},
		{CueRef: config.CueRef{Name: "b", Nature: "action", Shell: "run-b",
			Rollback: &config.CueRollback{Enabled: true, Defer: true}}},
		{CueRef: config.CueRef{Name: "c", Nature: "action",
			Rollback: &config.CueRollback{Enabled: true, Shell: "undo-c"}}},
	}
	results := []cue.Result{
		{CueName: "a", Status: cue.StatusChanged},
		{CueName: "b", Status: cue.StatusChanged},
		{CueName: "c", Status: cue.StatusChanged},
	}

	out := executeRollback(context.Background(), conn, cfg, nil,
		"v20260607-fail", dir, config.Target{}, Dispatch{}, nil, results, steps)

	if out.Err != nil {
		t.Fatalf("unexpected error: %v", out.Err)
	}
	// Expect: undo-c (reverse), undo-a (reverse), run-b (deferred, forward)
	want := []string{"undo-c", "undo-a", "run-b"}
	if len(runCmds) != 3 || runCmds[0] != want[0] || runCmds[1] != want[1] || runCmds[2] != want[2] {
		t.Errorf("expected order %v, got %v", want, runCmds)
	}
}

// TestExecuteRollback_defer_multipleRunInForwardOrder verifies multiple deferred
// cues run in their original forward execution order.
func TestExecuteRollback_defer_multipleRunInForwardOrder(t *testing.T) {
	dir := t.TempDir()
	prevID := "v20260601-120000"
	makeTestSnapshot(t, dir, prevID, ReleaseManifest{Release: prevID}, nil)

	var runCmds []string
	conn := &mockSSHConn{runFn: func(cmd string) { runCmds = append(runCmds, cmd) }}

	cfg := &config.Config{Scenarios: map[string]config.Scenario{"Deploy": {}}}
	steps := []Step{
		{CueRef: config.CueRef{Name: "x", Nature: "action", Shell: "run-x",
			Rollback: &config.CueRollback{Enabled: true, Defer: true}}},
		{CueRef: config.CueRef{Name: "y", Nature: "action", Shell: "run-y",
			Rollback: &config.CueRollback{Enabled: true, Defer: true}}},
	}
	results := []cue.Result{
		{CueName: "x", Status: cue.StatusChanged},
		{CueName: "y", Status: cue.StatusChanged},
	}

	out := executeRollback(context.Background(), conn, cfg, nil,
		"v20260607-fail", dir, config.Target{}, Dispatch{}, nil, results, steps)

	if out.Err != nil {
		t.Fatalf("unexpected error: %v", out.Err)
	}
	// Forward order: x then y (not reversed).
	want := []string{"run-x", "run-y"}
	if len(runCmds) != 2 || runCmds[0] != want[0] || runCmds[1] != want[1] {
		t.Errorf("expected forward order %v, got %v", want, runCmds)
	}
}

func TestExecuteRollback_packNature_restoresAllFiles(t *testing.T) {
	dir := t.TempDir()
	prevID := "v20260601-120000"
	makeTestSnapshot(t, dir, prevID, ReleaseManifest{
		Release: prevID,
		CueArtifacts: map[string]map[string]string{
			"frontend": {
				"frontend/index.html": "/var/www/index.html",
				"frontend/app.js":     "/var/www/app.js",
			},
		},
	}, map[string][]byte{
		"frontend/index.html": []byte("<html>prev</html>"),
		"frontend/app.js":     []byte("// prev js"),
	})

	uploads := make(map[string][]byte)
	conn := &mockSSHConn{uploadFn: func(data []byte, remote string) { uploads[remote] = data }}

	cfg := &config.Config{Scenarios: map[string]config.Scenario{"App": {}}}
	steps := []Step{{CueRef: config.CueRef{Name: "frontend", Nature: "pack",
		Rollback: &config.CueRollback{Enabled: true}}, OnErrorScenario: "App"}}
	results := []cue.Result{{CueName: "frontend", Status: cue.StatusChanged}}

	out := executeRollback(context.Background(), conn, cfg, []string{"App"},
		"v20260607-fail", dir, config.Target{}, Dispatch{}, nil, results, steps)

	if out.Err != nil {
		t.Fatalf("unexpected error: %v", out.Err)
	}
	if string(uploads["/var/www/index.html"]) != "<html>prev</html>" {
		t.Errorf("index.html not restored: %v", uploads)
	}
	if string(uploads["/var/www/app.js"]) != "// prev js" {
		t.Errorf("app.js not restored: %v", uploads)
	}
}
