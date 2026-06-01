// internal/runner/runner_test.go
package runner_test

import (
	"context"
	"testing"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
	"git.disroot.org/jmy/regis/internal/runner"
)

// mockExec is a minimal cue.Executor that returns a fixed result.
type mockExec struct {
	nature string
	status cue.Status
}

func (m *mockExec) Execute(_ context.Context, _ cue.SSHConn, cr config.CueRef, _ config.Target) (cue.Result, error) {
	return cue.Result{CueName: cr.Name, Nature: m.nature, Status: m.status}, nil
}

// makeAllNaturesCfg builds a config covering all 7 natures.
// In DryRun, local non-generate cues (action Local:true) are skipped by the runner,
// so expected result count is 6 (generate + 4 remote from app + render from tasks).
func makeAllNaturesCfg() (*config.Config, runner.Dispatch) {
	cfg := &config.Config{
		Targets: []config.Target{{Name: "prod", Host: "h", User: "u", Dir: "/opt"}},
		Scenarios: map[string]config.Scenario{
			"app": {Cues: []config.CueRef{
				{Name: "bin", Nature: "binary"},
				{Name: "cfg", Nature: "config"},
				{Name: "sec", Nature: "secret"},
				{Name: "svc", Nature: "service", Manager: "systemd"},
			}},
			"tasks": {Cues: []config.CueRef{
				{Name: "gen", Nature: "generate"},
				{Name: "rnd", Nature: "render"},
				{Name: "act", Nature: "action", Local: true}, // skipped in dry-run
			}},
		},
		ScenarioNames: []string{"app", "tasks"},
	}
	dispatch := runner.Dispatch{
		Binary:   &mockExec{"binary", cue.StatusChanged},
		Config:   &mockExec{"config", cue.StatusEqual},
		Secret:   &mockExec{"secret", cue.StatusEqual},
		Action:   &mockExec{"action", cue.StatusChanged},
		Generate: &mockExec{"generate", cue.StatusEqual},
		Render:   &mockExec{"render", cue.StatusChanged},
		Service:  &mockExec{"service", cue.StatusChanged},
	}
	return cfg, dispatch
}

func TestRun_dryRun_allNatures(t *testing.T) {
	cfg, dispatch := makeAllNaturesCfg()
	tgt := cfg.Targets[0]

	result, err := runner.Run(context.Background(), cfg, cfg.ScenarioNames, tgt,
		runner.Options{DryRun: true}, dispatch, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// 6 results: generate(local→Phase1a) + binary+config+secret+service+render(remote)
	// action(local:true) is skipped in dry-run by Phase1b guard.
	if len(result.Results) != 6 {
		t.Errorf("want 6 results, got %d", len(result.Results))
	}

	// tallyCounts: binary+service+render changed=3; generate+config+secret equal=3
	if result.Changed != 3 {
		t.Errorf("want 3 changed, got %d", result.Changed)
	}
	if result.Equal != 3 {
		t.Errorf("want 3 equal, got %d", result.Equal)
	}
}

func TestRun_dryRun_onResultCallback(t *testing.T) {
	cfg, dispatch := makeAllNaturesCfg()
	tgt := cfg.Targets[0]
	var seen []string
	result, err := runner.Run(context.Background(), cfg, cfg.ScenarioNames, tgt,
		runner.Options{DryRun: true}, dispatch, func(r cue.Result) {
			seen = append(seen, r.CueName)
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(seen) != len(result.Results) {
		t.Errorf("onResult called %d times, want %d", len(seen), len(result.Results))
	}
}

func TestRun_natureFilter(t *testing.T) {
	cfg, dispatch := makeAllNaturesCfg()
	tgt := cfg.Targets[0]

	result, err := runner.Run(context.Background(), cfg, cfg.ScenarioNames, tgt,
		runner.Options{DryRun: true, NatureFilter: []string{"binary", "config"}},
		dispatch, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Results) != 2 {
		t.Errorf("want 2 results (binary+config), got %d", len(result.Results))
	}
	for _, r := range result.Results {
		if r.Nature != "binary" && r.Nature != "config" {
			t.Errorf("unexpected nature %q in filtered results", r.Nature)
		}
	}
}

func TestRun_generateAlwaysRunsInDryRun(t *testing.T) {
	cfg := &config.Config{
		Targets: []config.Target{{Name: "prod", Host: "h", User: "u", Dir: "/opt"}},
		Scenarios: map[string]config.Scenario{
			"s": {Cues: []config.CueRef{
				{Name: "gen", Nature: "generate"},
				{Name: "bin", Nature: "binary"},
			}},
		},
		ScenarioNames: []string{"s"},
	}
	var genCalled bool
	dispatch := runner.Dispatch{
		Generate: &callTrackExec{nature: "generate", status: cue.StatusEqual, called: &genCalled},
		Binary:   &mockExec{"binary", cue.StatusEqual},
	}
	_, err := runner.Run(context.Background(), cfg, cfg.ScenarioNames, cfg.Targets[0],
		runner.Options{DryRun: true}, dispatch, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !genCalled {
		t.Error("generate executor must be called even in dry-run")
	}
}

func TestRun_natureFilter_empty_runsAll(t *testing.T) {
	cfg, dispatch := makeAllNaturesCfg()
	tgt := cfg.Targets[0]

	resultAll, _ := runner.Run(context.Background(), cfg, cfg.ScenarioNames, tgt,
		runner.Options{DryRun: true}, dispatch, nil)
	resultFiltered, _ := runner.Run(context.Background(), cfg, cfg.ScenarioNames, tgt,
		runner.Options{DryRun: true, NatureFilter: []string{}}, dispatch, nil)
	if len(resultAll.Results) != len(resultFiltered.Results) {
		t.Errorf("empty filter should run all: got %d vs %d",
			len(resultAll.Results), len(resultFiltered.Results))
	}
}

// TestRun_scenarioRef_expands verifies that { scenario: xxx } in a cue list
// inlines the referenced scenario's cues at that position.
func TestRun_scenarioRef_expands(t *testing.T) {
	cfg := &config.Config{
		Targets: []config.Target{{Name: "prod", Host: "h", User: "u", Dir: "/opt"}},
		Scenarios: map[string]config.Scenario{
			"app": {Cues: []config.CueRef{
				{Name: "bin", Nature: "binary"},
				{Name: "cfg", Nature: "config"},
			}},
			"deploy": {Cues: []config.CueRef{
				{ScenarioRef: "app"}, // inline ref — should expand to bin + cfg
				{Name: "migrate", Nature: "action"},
			}},
		},
		ScenarioNames: []string{"deploy"},
	}
	var seen []string
	dispatch := runner.Dispatch{
		Binary: &callTrackExec{nature: "binary", status: cue.StatusChanged, called: new(bool)},
		Config: &mockExec{"config", cue.StatusEqual},
		Action: &mockExec{"action", cue.StatusEqual},
	}
	_, err := runner.Run(context.Background(), cfg, []string{"deploy"}, cfg.Targets[0],
		runner.Options{DryRun: true}, dispatch, func(r cue.Result) {
			seen = append(seen, r.CueName)
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"bin", "cfg", "migrate"}
	if len(seen) != len(want) {
		t.Fatalf("want cues %v, got %v", want, seen)
	}
	for i, name := range want {
		if seen[i] != name {
			t.Errorf("position %d: want %q, got %q", i, name, seen[i])
		}
	}
}

// TestRun_scenarioRef_narrowCue verifies { scenario: x, cue: y } runs only cue y.
func TestRun_scenarioRef_narrowCue(t *testing.T) {
	cfg := &config.Config{
		Targets: []config.Target{{Name: "prod", Host: "h", User: "u", Dir: "/opt"}},
		Scenarios: map[string]config.Scenario{
			"app": {Cues: []config.CueRef{
				{Name: "bin", Nature: "binary"},
				{Name: "cfg", Nature: "config"},
			}},
			"deploy": {Cues: []config.CueRef{
				{ScenarioRef: "app", NarrowCue: "cfg"},
			}},
		},
		ScenarioNames: []string{"deploy"},
	}
	var seen []string
	dispatch := runner.Dispatch{
		Binary: &mockExec{"binary", cue.StatusEqual},
		Config: &mockExec{"config", cue.StatusEqual},
	}
	_, err := runner.Run(context.Background(), cfg, []string{"deploy"}, cfg.Targets[0],
		runner.Options{DryRun: true}, dispatch, func(r cue.Result) {
			seen = append(seen, r.CueName)
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(seen) != 1 || seen[0] != "cfg" {
		t.Errorf("want [cfg], got %v", seen)
	}
}

// TestRun_scenarioRef_orderPreserved verifies cues before and after an inline ref
// appear in the correct sequence.
func TestRun_scenarioRef_orderPreserved(t *testing.T) {
	cfg := &config.Config{
		Targets: []config.Target{{Name: "prod", Host: "h", User: "u", Dir: "/opt"}},
		Scenarios: map[string]config.Scenario{
			"files": {Cues: []config.CueRef{
				{Name: "upload", Nature: "config"},
			}},
			"deploy": {Cues: []config.CueRef{
				{Name: "offline", Nature: "action"},
				{ScenarioRef: "files"},
				{Name: "online", Nature: "action"},
			}},
		},
		ScenarioNames: []string{"deploy"},
	}
	var seen []string
	dispatch := runner.Dispatch{
		Config: &mockExec{"config", cue.StatusEqual},
		Action: &mockExec{"action", cue.StatusEqual},
	}
	_, err := runner.Run(context.Background(), cfg, []string{"deploy"}, cfg.Targets[0],
		runner.Options{DryRun: true}, dispatch, func(r cue.Result) {
			seen = append(seen, r.CueName)
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"offline", "upload", "online"}
	if len(seen) != len(want) {
		t.Fatalf("want %v, got %v", want, seen)
	}
	for i, name := range want {
		if seen[i] != name {
			t.Errorf("position %d: want %q, got %q", i, name, seen[i])
		}
	}
}

// callTrackExec records whether Execute was ever called.
type callTrackExec struct {
	nature string
	status cue.Status
	called *bool
}

func (c *callTrackExec) Execute(_ context.Context, _ cue.SSHConn, cr config.CueRef, _ config.Target) (cue.Result, error) {
	*c.called = true
	return cue.Result{CueName: cr.Name, Nature: c.nature, Status: c.status}, nil
}
