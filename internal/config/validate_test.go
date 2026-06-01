// internal/config/validate_test.go
package config_test

import (
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

func TestValidate_cueName_required(t *testing.T) {
	c := minimalCfg()
	c.Scenarios["s"] = config.Scenario{
		Cues: []config.CueRef{{Nature: "action", Shell: "echo hi"}},
	}
	if errs := config.Validate(c); len(errs) == 0 {
		t.Error("expected error: cue name required")
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
