// internal/config/validate_test.go
package config_test

import (
	"strings"
	"testing"

	"git.disroot.org/jmy/regis/internal/config"
)

func minimalCfg() *config.Config {
	return &config.Config{
		Targets:   []config.Target{{Name: "prod", Host: "h", User: "u", Dir: "/opt/app"}},
		Scenarios: map[string]config.Scenario{},
	}
}

func TestValidate_noTargets(t *testing.T) {
	errs := config.Validate(&config.Config{})
	if len(errs) == 0 {
		t.Error("expected error for no targets")
	}
}

func TestValidate_ok(t *testing.T) {
	c := minimalCfg()
	c.Scenarios["build"] = config.Scenario{
		Cues: []config.CueRef{{Name: "bins", Nature: "action", Local: true, Shell: "go build ./..."}},
	}
	if errs := config.Validate(c); len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
}

func TestValidate_natureInference_action(t *testing.T) {
	c := minimalCfg()
	c.Scenarios["s"] = config.Scenario{
		Cues: []config.CueRef{{Name: "x", Shell: "echo hi"}},
	}
	if errs := config.Validate(c); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if c.Scenarios["s"].Cues[0].Nature != "action" {
		t.Errorf("want nature=action inferred, got %q", c.Scenarios["s"].Cues[0].Nature)
	}
}

func TestValidate_natureInference_secret(t *testing.T) {
	c := minimalCfg()
	c.Scenarios["s"] = config.Scenario{
		Cues: []config.CueRef{{
			Name:     "env",
			Src:      config.StringOrList{".env"},
			Dest:     ".env",
			Preserve: config.StringOrList{"TOKEN"},
		}},
	}
	if errs := config.Validate(c); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if c.Scenarios["s"].Cues[0].Nature != "secret" {
		t.Errorf("want nature=secret inferred, got %q", c.Scenarios["s"].Cues[0].Nature)
	}
}

func TestValidate_srcWithoutNature_error(t *testing.T) {
	c := minimalCfg()
	c.Scenarios["s"] = config.Scenario{
		Cues: []config.CueRef{{Name: "bin", Src: config.StringOrList{"bin/saver"}, Dest: "saver"}},
	}
	if errs := config.Validate(c); len(errs) == 0 {
		t.Error("expected error: binary/config need explicit nature")
	}
}

func TestValidate_cueName_inferredFromScenario(t *testing.T) {
	// Single-cue scenario with no explicit name: infer name from scenario key.
	c := minimalCfg()
	c.Scenarios["my-cue"] = config.Scenario{
		Cues: []config.CueRef{{Nature: "action", Shell: "echo hi"}},
	}
	errs := config.Validate(c)
	if len(errs) != 0 {
		t.Errorf("expected no errors for single-cue inferred name, got: %v", errs)
	}
	if got := c.Scenarios["my-cue"].Cues[0].Name; got != "my-cue" {
		t.Errorf("inferred name = %q, want %q", got, "my-cue")
	}
}

func TestValidate_cueName_requiredForMultiCue(t *testing.T) {
	// Multi-cue scenario: unnamed cue must still error.
	c := minimalCfg()
	c.Scenarios["s"] = config.Scenario{
		Cues: []config.CueRef{
			{Name: "first", Nature: "action", Shell: "echo a"},
			{Nature: "action", Shell: "echo b"},
		},
	}
	if errs := config.Validate(c); len(errs) == 0 {
		t.Error("expected error: unnamed cue in multi-cue scenario")
	}
}

func TestValidate_knownNatures_allAccepted(t *testing.T) {
	// Every nature in knownNatures must pass validation without error.
	// This prevents the "forgot to add to allowlist" regression.
	for nature := range config.KnownNatures {
		c := minimalCfg()
		c.Scenarios["s"] = config.Scenario{
			Cues: []config.CueRef{{Name: "x", Nature: nature}},
		}
		if errs := config.Validate(c); len(errs) != 0 {
			t.Errorf("nature %q should be valid but got errors: %v", nature, errs)
		}
	}
}

func TestValidate_unknownNature_rejected(t *testing.T) {
	c := minimalCfg()
	c.Scenarios["s"] = config.Scenario{
		Cues: []config.CueRef{{Name: "x", Nature: "bogus"}},
	}
	if errs := config.Validate(c); len(errs) == 0 {
		t.Error("expected error for unknown nature")
	}
}

func TestValidate_gitTrue_infersPack(t *testing.T) {
	c := minimalCfg()
	c.Scenarios["s"] = config.Scenario{
		Cues: []config.CueRef{{Name: "app", Git: true, Dest: "/var/www/"}},
	}
	if errs := config.Validate(c); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if got := c.Scenarios["s"].Cues[0].Nature; got != "pack" {
		t.Errorf("want nature=pack inferred from git: true, got %q", got)
	}
}

func TestValidate_gitTrue_withSrc_error(t *testing.T) {
	c := minimalCfg()
	c.Scenarios["s"] = config.Scenario{
		Cues: []config.CueRef{{Name: "app", Git: true, Src: config.StringOrList{"dist/**"}, Dest: "/var/www/"}},
	}
	errs := config.Validate(c)
	if len(errs) == 0 {
		t.Fatal("expected error for git: true combined with src:")
	}
	if !strings.Contains(errs[0].Error(), "mutually exclusive") {
		t.Errorf("expected 'mutually exclusive' in error, got %q", errs[0])
	}
}

func TestValidate_gitTrue_wrongNature_error(t *testing.T) {
	c := minimalCfg()
	c.Scenarios["s"] = config.Scenario{
		Cues: []config.CueRef{{Name: "app", Nature: "config", Git: true, Dest: "/var/www/"}},
	}
	errs := config.Validate(c)
	if len(errs) == 0 {
		t.Fatal("expected error for git: true with nature: config")
	}
	if !strings.Contains(errs[0].Error(), "nature: pack") {
		t.Errorf("expected 'nature: pack' in error, got %q", errs[0])
	}
}

func TestValidate_cueRef_undefined_scenario(t *testing.T) {
	c := minimalCfg()
	c.Scenarios["deploy"] = config.Scenario{
		Cues: []config.CueRef{
			{ScenarioRef: "typo-scenario"},
		},
	}
	errs := config.Validate(c)
	if len(errs) == 0 {
		t.Fatal("expected error: cue references undefined scenario")
	}
	if !strings.Contains(errs[0].Error(), "typo-scenario") {
		t.Errorf("error should name the undefined scenario, got: %v", errs[0])
	}
}

func TestValidate_cueRef_valid_scenario(t *testing.T) {
	c := minimalCfg()
	c.Scenarios["base"] = config.Scenario{
		Cues: []config.CueRef{{Name: "x", Nature: "action", Shell: "echo hi"}},
	}
	c.Scenarios["deploy"] = config.Scenario{
		Cues: []config.CueRef{{ScenarioRef: "base"}},
	}
	if errs := config.Validate(c); len(errs) != 0 {
		t.Errorf("expected no errors for valid scenario ref, got: %v", errs)
	}
}

func TestValidate_checkRef_undefined_scenario(t *testing.T) {
	c := minimalCfg()
	c.Scenarios["deploy"] = config.Scenario{
		Cues:   []config.CueRef{{Name: "x", Nature: "action", Shell: "echo hi"}},
		Checks: []config.CueRef{{ScenarioRef: "no-such-scenario"}},
	}
	errs := config.Validate(c)
	if len(errs) == 0 {
		t.Fatal("expected error: check references undefined scenario")
	}
	if !strings.Contains(errs[0].Error(), "no-such-scenario") {
		t.Errorf("error should name the undefined scenario, got: %v", errs[0])
	}
}

func TestValidate_rollbackRef_undefined_scenario(t *testing.T) {
	c := minimalCfg()
	c.Scenarios["deploy"] = config.Scenario{
		Cues:     []config.CueRef{{Name: "x", Nature: "action", Shell: "echo hi"}},
		Restore: []config.CueRef{{ScenarioRef: "no-such-scenario"}},
	}
	errs := config.Validate(c)
	if len(errs) == 0 {
		t.Fatal("expected error: rollback references undefined scenario")
	}
	if !strings.Contains(errs[0].Error(), "no-such-scenario") {
		t.Errorf("error should name the undefined scenario, got: %v", errs[0])
	}
}

func TestValidate_unknownRequires(t *testing.T) {
	c := minimalCfg()
	c.Scenarios["deploy"] = config.Scenario{
		Requires: config.StringOrList{"build"},
		Cues:     []config.CueRef{{Name: "x", Nature: "action", Shell: "echo"}},
	}
	if errs := config.Validate(c); len(errs) == 0 {
		t.Error("expected error: 'build' not defined")
	}
}
