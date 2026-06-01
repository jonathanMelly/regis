// internal/runner/env_test.go
package runner

import "testing"

func TestMergeEnv_nilLayers(t *testing.T) {
	if got := mergeEnv(nil, nil); got != nil {
		t.Errorf("want nil for all-empty layers, got %v", got)
	}
}

func TestMergeEnv_defaultsOnly(t *testing.T) {
	defaults := map[string]string{"GOOS": "linux", "CGO_ENABLED": "0"}
	got := mergeEnv(defaults, nil, nil)
	if got["GOOS"] != "linux" || got["CGO_ENABLED"] != "0" {
		t.Errorf("unexpected: %v", got)
	}
}

func TestMergeEnv_scenarioOverridesDefaults(t *testing.T) {
	defaults := map[string]string{"A": "default", "B": "default"}
	scenario := map[string]string{"B": "scenario", "C": "scenario"}
	got := mergeEnv(defaults, scenario, nil)
	if got["A"] != "default" {
		t.Errorf("A: want default, got %q", got["A"])
	}
	if got["B"] != "scenario" {
		t.Errorf("B: want scenario (scenario overrides defaults), got %q", got["B"])
	}
	if got["C"] != "scenario" {
		t.Errorf("C: want scenario, got %q", got["C"])
	}
}

func TestMergeEnv_cueOverridesAll(t *testing.T) {
	defaults := map[string]string{"A": "default", "B": "default"}
	scenario := map[string]string{"B": "scenario", "C": "scenario"}
	cueEnv := map[string]string{"C": "cue", "D": "cue"}
	got := mergeEnv(defaults, scenario, cueEnv)
	if got["A"] != "default" {
		t.Errorf("A: want default, got %q", got["A"])
	}
	if got["B"] != "scenario" {
		t.Errorf("B: want scenario, got %q", got["B"])
	}
	if got["C"] != "cue" {
		t.Errorf("C: want cue (cue overrides scenario), got %q", got["C"])
	}
	if got["D"] != "cue" {
		t.Errorf("D: want cue, got %q", got["D"])
	}
}
