// cmd/regis/cmd/run_test.go
package cmd_test

import (
	"strings"
	"testing"

	"git.disroot.org/jmy/regis/cmd/regis/cmd"
)

func TestRunCommand_reservedNames(t *testing.T) {
	reserved := []string{"config", "init", "score", "show", "fetch", "rdiff",
		"status", "release", "releases", "service", "ssh", "exec"}
	for _, name := range reserved {
		if !cmd.IsReservedScenarioName(name) {
			t.Errorf("expected %q to be reserved", name)
		}
	}
}

func TestRunCommand_colonPrefix_notReserved(t *testing.T) {
	if cmd.IsReservedScenarioName(":fetch") {
		t.Error(":fetch should not match reserved (colon prefix = built-in override)")
	}
}

func TestParseNatureFilter(t *testing.T) {
	natures := cmd.ParseNatureFilter("binary,secret")
	if len(natures) != 2 {
		t.Fatalf("want 2, got %v", natures)
	}
	if natures[0] != "binary" || natures[1] != "secret" {
		t.Errorf("unexpected natures: %v", natures)
	}
}

func TestParseNatureFilter_s_shorthand(t *testing.T) {
	natures := cmd.ParseNatureFilter("secret")
	if len(natures) != 1 || natures[0] != "secret" {
		t.Errorf("unexpected: %v", natures)
	}
}

func TestParseRunArgs_plain_scenario(t *testing.T) {
	names, scoped, err := cmd.ParseRunArgs("app,tasks")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "app" || names[1] != "tasks" {
		t.Errorf("want [app tasks], got %v", names)
	}
	if len(scoped) != 0 {
		t.Errorf("want no scoped cues, got %v", scoped)
	}
}

func TestParseRunArgs_scoped_cue(t *testing.T) {
	names, scoped, err := cmd.ParseRunArgs("app:cfg")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "app" {
		t.Errorf("want [app], got %v", names)
	}
	if cues := scoped["app"]; len(cues) != 1 || cues[0] != "cfg" {
		t.Errorf("want scoped[app]=[cfg], got %v", scoped)
	}
}

func TestParseRunArgs_mixed(t *testing.T) {
	names, scoped, err := cmd.ParseRunArgs("app:cfg,tasks")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 {
		t.Fatalf("want 2 scenario names, got %v", names)
	}
	if _, ok := scoped["app"]; !ok {
		t.Error("want app in scoped cues")
	}
	if _, ok := scoped["tasks"]; ok {
		t.Error("tasks should not be in scoped cues (unscoped scenario)")
	}
}

func TestParseRunArgs_multi_cues_same_scenario(t *testing.T) {
	names, scoped, err := cmd.ParseRunArgs("app:bin,app:cfg")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "app" {
		t.Errorf("want deduplicated [app], got %v", names)
	}
	if cues := scoped["app"]; len(cues) != 2 {
		t.Errorf("want 2 cues for app, got %v", cues)
	}
}

func TestParseRunArgs_malformed_missing_cue(t *testing.T) {
	_, _, err := cmd.ParseRunArgs("app:")
	if err == nil {
		t.Error("expected error for 'app:' (missing cue name)")
	}
	if !strings.Contains(err.Error(), "app:") {
		t.Errorf("error should mention the bad token, got: %v", err)
	}
}

func TestParseRunArgs_malformed_missing_scenario(t *testing.T) {
	_, _, err := cmd.ParseRunArgs(":cue")
	if err == nil {
		t.Error("expected error for ':cue' (missing scenario name)")
	}
}

func TestTargetSelector_all(t *testing.T) {
	targets := []string{"prod-eu", "prod-us", "staging"}
	sel := cmd.SelectTargets(targets, "all")
	if len(sel) != 3 {
		t.Errorf("want all 3, got %v", sel)
	}
}

func TestTargetSelector_glob(t *testing.T) {
	targets := []string{"prod-eu", "prod-us", "staging"}
	sel := cmd.SelectTargets(targets, "prod*")
	if len(sel) != 2 {
		t.Errorf("want prod-eu + prod-us, got %v", sel)
	}
	for _, t2 := range sel {
		if !strings.HasPrefix(t2, "prod") {
			t.Errorf("unexpected target: %s", t2)
		}
	}
}
