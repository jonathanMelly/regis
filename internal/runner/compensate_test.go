// internal/runner/compensate_test.go
package runner

import (
	"context"
	"fmt"
	"io/fs"
	"testing"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
)

// ── test helpers ──────────────────────────────────────────────────────────────

type funcExecutor struct {
	fn func(config.CueRef) cue.Result
}

func (e *funcExecutor) Execute(_ context.Context, _ cue.SSHConn, cr config.CueRef, _ config.Target) (cue.Result, error) {
	return e.fn(cr), nil
}

var _ cue.Executor = (*funcExecutor)(nil)

type mockSSHConn struct {
	runFn     func(cmd string)
	runSudoFn func(cmd string)
	runErr    error
}

func (m *mockSSHConn) Upload(_, _ string, _ fs.FileMode, _ bool) error { return nil }
func (m *mockSSHConn) UploadBytes(_ []byte, _ string, _ fs.FileMode, _ bool) error {
	return nil
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
func (m *mockSSHConn) RunWithEnv(_ string, _ map[string]string) (string, string, int, error) {
	return "", "", 0, nil
}
func (m *mockSSHConn) Download(_ string) ([]byte, error) { return nil, nil }
func (m *mockSSHConn) Exists(_ string) (bool, error)     { return false, nil }
func (m *mockSSHConn) PathSep() string                   { return "/" }
func (m *mockSSHConn) RunStream(_ string, _ func(string, bool)) (string, string, int, error) {
	return "", "", 0, nil
}

var _ cue.SSHConn = (*mockSSHConn)(nil)

// ── effectiveOnError ──────────────────────────────────────────────────────────

func TestEffectiveOnError_scenarioOverride(t *testing.T) {
	cfg := &config.Config{
		Scenarios: map[string]config.Scenario{"Deploy": {OnError: "compensate"}},
		Run:       config.RunConfig{OnError: "halt"},
	}
	if got := effectiveOnError(cfg, "Deploy"); got != "compensate" {
		t.Errorf("expected compensate, got %q", got)
	}
}

func TestEffectiveOnError_restoreAliasNormalized(t *testing.T) {
	cfg := &config.Config{
		Scenarios: map[string]config.Scenario{"Deploy": {OnError: "restore"}},
		Run:       config.RunConfig{OnError: "halt"},
	}
	if got := effectiveOnError(cfg, "Deploy"); got != "compensate" {
		t.Errorf("expected compensate (normalized from restore), got %q", got)
	}
}

func TestEffectiveOnError_globalFallback(t *testing.T) {
	cfg := &config.Config{
		Scenarios: map[string]config.Scenario{"Setup": {}},
		Run:       config.RunConfig{OnError: "continue"},
	}
	if got := effectiveOnError(cfg, "Setup"); got != "continue" {
		t.Errorf("expected continue, got %q", got)
	}
}

func TestEffectiveOnError_defaultHalt(t *testing.T) {
	cfg := &config.Config{Scenarios: map[string]config.Scenario{"Setup": {}}}
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

func TestEffectiveOnError_inferredFromCueCompensation(t *testing.T) {
	cfg := &config.Config{
		Scenarios: map[string]config.Scenario{
			"Deploy": {
				Cues: []config.CueRef{
					{Name: "bin", Nature: "binary", Compensation: &config.CueCompensation{Enabled: true}},
				},
			},
		},
	}
	if got := effectiveOnError(cfg, "Deploy"); got != "compensate" {
		t.Errorf("expected compensate inferred from cue, got %q", got)
	}
}

func TestEffectiveOnError_explicitOverridesInferred(t *testing.T) {
	cfg := &config.Config{
		Scenarios: map[string]config.Scenario{
			"Deploy": {
				OnError: "continue",
				Cues:    []config.CueRef{{Name: "bin", Compensation: &config.CueCompensation{Enabled: true}}},
			},
		},
	}
	if got := effectiveOnError(cfg, "Deploy"); got != "continue" {
		t.Errorf("expected continue (explicit), got %q", got)
	}
}

func TestEffectiveOnError_disabledCompensationNotInferred(t *testing.T) {
	cfg := &config.Config{
		Scenarios: map[string]config.Scenario{
			"Deploy": {
				Cues: []config.CueRef{{Name: "bin", Compensation: &config.CueCompensation{Enabled: false}}},
			},
		},
	}
	if got := effectiveOnError(cfg, "Deploy"); got != "halt" {
		t.Errorf("expected halt (disabled compensation not inferred), got %q", got)
	}
}

// ── failingScenarioName ───────────────────────────────────────────────────────

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

func TestFailingScenarioName_inlineRef(t *testing.T) {
	results := []cue.Result{{Status: cue.StatusFailed}}
	steps := []Step{{ScenarioName: "app", OnErrorScenario: "deploy"}}
	if got := failingScenarioName(results, steps); got != "deploy" {
		t.Errorf("expected deploy (parent), got %q", got)
	}
}

// ── executeCompensation ───────────────────────────────────────────────────────

func compensateCall(
	conn cue.SSHConn,
	cfg *config.Config,
	order []string,
	deployID string,
	dispatch Dispatch,
	results []cue.Result,
	steps []Step,
) *CompensateOutcome {
	return executeCompensation(context.Background(), conn, cfg, order, deployID,
		config.Target{}, dispatch, nil, results, steps, nil)
}

func TestExecuteCompensation_actionShellRuns(t *testing.T) {
	var runCmds []string
	conn := &mockSSHConn{runFn: func(cmd string) { runCmds = append(runCmds, cmd) }}

	cfg := &config.Config{Scenarios: map[string]config.Scenario{"Deploy": {}}}
	steps := []Step{{CueRef: config.CueRef{Name: "go-offline", Nature: "action",
		Compensation: &config.CueCompensation{Enabled: true, Shell: "rm -f maintenance.flag"}},
		OnErrorScenario: "Deploy"}}
	results := []cue.Result{{CueName: "go-offline", Status: cue.StatusChanged}}

	out := compensateCall(conn, cfg, []string{"Deploy"}, "v-fail", Dispatch{}, results, steps)
	if out.Err != nil {
		t.Fatalf("unexpected error: %v", out.Err)
	}
	if len(runCmds) != 1 || runCmds[0] != "rm -f maintenance.flag" {
		t.Errorf("expected compensation command, got %v", runCmds)
	}
}

func TestExecuteCompensation_sudoUsesRunSudo(t *testing.T) {
	var sudoCmds []string
	conn := &mockSSHConn{runSudoFn: func(cmd string) { sudoCmds = append(sudoCmds, cmd) }}

	cfg := &config.Config{Scenarios: map[string]config.Scenario{"Deploy": {}}}
	steps := []Step{{CueRef: config.CueRef{Name: "stop-svc", Nature: "action",
		Compensation: &config.CueCompensation{Enabled: true, Shell: "systemctl stop myapp", Sudo: true}},
		OnErrorScenario: "Deploy"}}
	results := []cue.Result{{CueName: "stop-svc", Status: cue.StatusChanged}}

	out := compensateCall(conn, cfg, []string{"Deploy"}, "v-fail", Dispatch{}, results, steps)
	if out.Err != nil {
		t.Fatalf("unexpected error: %v", out.Err)
	}
	if len(sudoCmds) != 1 || sudoCmds[0] != "systemctl stop myapp" {
		t.Errorf("expected sudo command, got %v", sudoCmds)
	}
}

func TestExecuteCompensation_reverseOrder(t *testing.T) {
	var order []string
	conn := &mockSSHConn{runFn: func(cmd string) { order = append(order, cmd) }}

	cfg := &config.Config{Scenarios: map[string]config.Scenario{"Deploy": {}}}
	steps := []Step{
		{CueRef: config.CueRef{Name: "a", Nature: "action", Compensation: &config.CueCompensation{Enabled: true, Shell: "undo-a"}}},
		{CueRef: config.CueRef{Name: "b", Nature: "action", Compensation: &config.CueCompensation{Enabled: true, Shell: "undo-b"}}},
		{CueRef: config.CueRef{Name: "c", Nature: "action", Compensation: &config.CueCompensation{Enabled: true, Shell: "undo-c"}}},
	}
	results := []cue.Result{
		{CueName: "a", Status: cue.StatusChanged},
		{CueName: "b", Status: cue.StatusChanged},
		{CueName: "c", Status: cue.StatusChanged},
	}

	out := compensateCall(conn, cfg, nil, "v-fail", Dispatch{}, results, steps)
	if out.Err != nil {
		t.Fatalf("unexpected error: %v", out.Err)
	}
	want := []string{"undo-c", "undo-b", "undo-a"}
	if len(order) != 3 || order[0] != want[0] || order[1] != want[1] || order[2] != want[2] {
		t.Errorf("expected reverse order %v, got %v", want, order)
	}
}

func TestExecuteCompensation_skippedCuesExcluded(t *testing.T) {
	var runCmds []string
	conn := &mockSSHConn{runFn: func(cmd string) { runCmds = append(runCmds, cmd) }}

	cfg := &config.Config{Scenarios: map[string]config.Scenario{"Deploy": {}}}
	steps := []Step{
		{CueRef: config.CueRef{Name: "skipped-cue", Nature: "action",
			Compensation: &config.CueCompensation{Enabled: true, Shell: "should-not-run"}}},
		{CueRef: config.CueRef{Name: "ran-cue", Nature: "action",
			Compensation: &config.CueCompensation{Enabled: true, Shell: "should-run"}}},
	}
	results := []cue.Result{
		{CueName: "skipped-cue", Status: cue.StatusSkipped},
		{CueName: "ran-cue", Status: cue.StatusChanged},
	}

	out := compensateCall(conn, cfg, nil, "v-fail", Dispatch{}, results, steps)
	if out.Err != nil {
		t.Fatalf("unexpected error: %v", out.Err)
	}
	if len(runCmds) != 1 || runCmds[0] != "should-run" {
		t.Errorf("expected only ran-cue compensated; got %v", runCmds)
	}
}

func TestExecuteCompensation_fileNature_noShell_skipped(t *testing.T) {
	// File natures with compensation: true (no shell) are silently skipped.
	var runCmds []string
	conn := &mockSSHConn{runFn: func(cmd string) { runCmds = append(runCmds, cmd) }}

	cfg := &config.Config{Scenarios: map[string]config.Scenario{"Deploy": {}}}
	steps := []Step{
		{CueRef: config.CueRef{Name: "app-binary", Nature: "binary",
			Compensation: &config.CueCompensation{Enabled: true}}}, // no Shell
	}
	results := []cue.Result{{CueName: "app-binary", Status: cue.StatusChanged}}

	out := compensateCall(conn, cfg, nil, "v-fail", Dispatch{}, results, steps)
	if out.Err != nil {
		t.Fatalf("unexpected error: %v", out.Err)
	}
	if len(runCmds) != 0 {
		t.Errorf("file nature without shell should not trigger any command, got %v", runCmds)
	}
}

func TestExecuteCompensation_actionError_stopsCompensation(t *testing.T) {
	conn := &mockSSHConn{runErr: fmt.Errorf("connection lost")}

	cfg := &config.Config{Scenarios: map[string]config.Scenario{"Deploy": {}}}
	steps := []Step{{CueRef: config.CueRef{Name: "go-offline", Nature: "action",
		Compensation: &config.CueCompensation{Enabled: true, Shell: "some-cmd"}},
		OnErrorScenario: "Deploy"}}
	results := []cue.Result{{CueName: "go-offline", Status: cue.StatusChanged}}

	out := compensateCall(conn, cfg, nil, "v-fail", Dispatch{}, results, steps)
	if out.Err == nil {
		t.Error("expected error when action compensation fails")
	}
}

func TestExecuteCompensation_scenarioCompensateRuns(t *testing.T) {
	var compensateCues []string
	exec := &funcExecutor{fn: func(cr config.CueRef) cue.Result {
		compensateCues = append(compensateCues, cr.Name)
		return cue.Result{CueName: cr.Name, Status: cue.StatusChanged}
	}}

	cfg := &config.Config{
		Scenarios: map[string]config.Scenario{
			"Deploy": {
				Compensate: []config.CueRef{
					{Name: "restore-db", Shell: "php artisan db:restore --label=pre-${STATE_ID}"},
				},
			},
		},
	}
	steps := []Step{{CueRef: config.CueRef{Name: "bin", Nature: "binary"}, OnErrorScenario: "Deploy"}}
	results := []cue.Result{{CueName: "bin", Status: cue.StatusChanged}}

	out := compensateCall(&mockSSHConn{}, cfg, []string{"Deploy"}, "v-fail",
		Dispatch{Action: exec}, results, steps)
	if out.Err != nil {
		t.Fatalf("unexpected error: %v", out.Err)
	}
	if len(compensateCues) != 1 || compensateCues[0] != "restore-db" {
		t.Errorf("expected scenario-level compensate cue to run, got %v", compensateCues)
	}
}

func TestExecuteCompensation_defer_runsAfterRegular(t *testing.T) {
	var order []string
	conn := &mockSSHConn{runFn: func(cmd string) { order = append(order, cmd) }}

	cfg := &config.Config{Scenarios: map[string]config.Scenario{"Deploy": {}}}
	steps := []Step{
		{CueRef: config.CueRef{Name: "a", Nature: "action",
			Compensation: &config.CueCompensation{Enabled: true, Shell: "undo-a"}}},
		{CueRef: config.CueRef{Name: "b", Nature: "action", Shell: "run-b",
			Compensation: &config.CueCompensation{Enabled: true, Defer: true}}},
		{CueRef: config.CueRef{Name: "c", Nature: "action",
			Compensation: &config.CueCompensation{Enabled: true, Shell: "undo-c"}}},
	}
	results := []cue.Result{
		{CueName: "a", Status: cue.StatusChanged},
		{CueName: "b", Status: cue.StatusChanged},
		{CueName: "c", Status: cue.StatusChanged},
	}

	out := compensateCall(conn, cfg, nil, "v-fail", Dispatch{}, results, steps)
	if out.Err != nil {
		t.Fatalf("unexpected error: %v", out.Err)
	}
	// Regular compensations in reverse: undo-c, undo-a. Deferred: run-b.
	want := []string{"undo-c", "undo-a", "run-b"}
	if len(order) != 3 || order[0] != want[0] || order[1] != want[1] || order[2] != want[2] {
		t.Errorf("expected order %v, got %v", want, order)
	}
}

func TestExecuteCompensation_defer_multipleRunInForwardOrder(t *testing.T) {
	var order []string
	conn := &mockSSHConn{runFn: func(cmd string) { order = append(order, cmd) }}

	cfg := &config.Config{Scenarios: map[string]config.Scenario{"Deploy": {}}}
	steps := []Step{
		{CueRef: config.CueRef{Name: "x", Nature: "action", Shell: "run-x",
			Compensation: &config.CueCompensation{Enabled: true, Defer: true}}},
		{CueRef: config.CueRef{Name: "y", Nature: "action", Shell: "run-y",
			Compensation: &config.CueCompensation{Enabled: true, Defer: true}}},
	}
	results := []cue.Result{
		{CueName: "x", Status: cue.StatusChanged},
		{CueName: "y", Status: cue.StatusChanged},
	}

	out := compensateCall(conn, cfg, nil, "v-fail", Dispatch{}, results, steps)
	if out.Err != nil {
		t.Fatalf("unexpected error: %v", out.Err)
	}
	// Both deferred: run in forward order x, y.
	want := []string{"run-x", "run-y"}
	if len(order) != 2 || order[0] != want[0] || order[1] != want[1] {
		t.Errorf("expected forward order %v, got %v", want, order)
	}
}
