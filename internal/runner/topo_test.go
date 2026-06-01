// internal/runner/topo_test.go
package runner_test

import (
	"testing"
	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/runner"
)

func TestTopoSort_simple(t *testing.T) {
	scenarios := map[string]config.Scenario{
		"build":   {Requires: config.StringOrList{}},
		"checks":  {Requires: config.StringOrList{}},
		"saver":   {Requires: config.StringOrList{"build"}},
		"full":    {Requires: config.StringOrList{}},
	}
	order, err := runner.TopoSort(scenarios, []string{"saver"})
	if err != nil {
		t.Fatal(err)
	}
	buildIdx, saverIdx := -1, -1
	for i, name := range order {
		switch name {
		case "build":
			buildIdx = i
		case "saver":
			saverIdx = i
		}
	}
	if buildIdx == -1 || saverIdx == -1 {
		t.Fatalf("missing entries: %v", order)
	}
	if buildIdx >= saverIdx {
		t.Errorf("build must come before saver; order: %v", order)
	}
}

func TestTopoSort_cycle(t *testing.T) {
	scenarios := map[string]config.Scenario{
		"a": {Requires: config.StringOrList{"b"}},
		"b": {Requires: config.StringOrList{"a"}},
	}
	_, err := runner.TopoSort(scenarios, []string{"a"})
	if err == nil {
		t.Error("expected cycle error")
	}
}

func TestTopoSort_deduplicatesRequires(t *testing.T) {
	scenarios := map[string]config.Scenario{
		"build":     {Requires: config.StringOrList{}},
		"saver":     {Requires: config.StringOrList{"build"}},
		"dashboard": {Requires: config.StringOrList{"build"}},
		"full":      {Requires: config.StringOrList{}},
	}
	// Running full should include build only once
	order, err := runner.TopoSort(scenarios, []string{"saver", "dashboard"})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, n := range order {
		if n == "build" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("build appears %d times (want 1): %v", count, order)
	}
}
