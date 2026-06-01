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
