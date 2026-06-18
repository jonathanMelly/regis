// internal/config/validate_yaml_test.go
// End-to-end tests for validation via real YAML loading.
// These complement the unit tests in validate_test.go which build configs programmatically
// and therefore bypass the YAML parsing + nature-inference pipeline.
package config_test

import (
	"path/filepath"
	"strings"
	"testing"

	"git.disroot.org/jmy/regis/internal/config"
)

const baseTargetYAML = `
targets:
  - name: prod
    host: prod.example.com
    user: deploy
    dir: /opt/app
`

func loadYAML(t *testing.T, body string) (*config.Config, error) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "regis.yml", baseTargetYAML+body)
	return config.Load(filepath.Join(dir, "regis.yml"))
}

// mustLoad fails the test if loading/validation returns an error.
func mustLoad(t *testing.T, body string) *config.Config {
	t.Helper()
	cfg, err := loadYAML(t, body)
	if err != nil {
		t.Fatalf("unexpected load error: %v", err)
	}
	return cfg
}

// mustFail fails the test if loading/validation succeeds; returns the error string.
func mustFail(t *testing.T, body string) string {
	t.Helper()
	_, err := loadYAML(t, body)
	if err == nil {
		t.Fatal("expected validation error, got none")
	}
	return err.Error()
}

// ── service cue: nature inferred from manager ─────────────────────────────────

func TestValidateYAML_service_managerInfersNature(t *testing.T) {
	cfg := mustLoad(t, `
scenarios:
  deploy:
    cues:
      - name: saver
        manager: crontab
        binary: saver
`)
	cr := cfg.Scenarios["deploy"].Cues[0]
	if cr.Nature != "service" {
		t.Errorf("want nature=service inferred from manager, got %q", cr.Nature)
	}
	if cr.Manager != "crontab" {
		t.Errorf("want Manager=crontab, got %q", cr.Manager)
	}
	if cr.Binary != "saver" {
		t.Errorf("want Binary=saver, got %q", cr.Binary)
	}
}

func TestValidateYAML_service_explicitNature(t *testing.T) {
	cfg := mustLoad(t, `
scenarios:
  deploy:
    cues:
      - name: mailway
        nature: service
        manager: systemd
        service_file: mailway/mailway.service
`)
	cr := cfg.Scenarios["deploy"].Cues[0]
	if cr.Nature != "service" {
		t.Errorf("want nature=service, got %q", cr.Nature)
	}
	if cr.ServiceFile != "mailway/mailway.service" {
		t.Errorf("want ServiceFile set, got %q", cr.ServiceFile)
	}
}

func TestValidateYAML_service_commandsOverride(t *testing.T) {
	cfg := mustLoad(t, `
scenarios:
  deploy:
    cues:
      - name: nginx-front
        manager: systemd
        sudo: true
        service_name: nginx-front
        commands:
          reload: nginx -t && {reload}
`)
	cr := cfg.Scenarios["deploy"].Cues[0]
	if cr.Nature != "service" {
		t.Errorf("want nature=service, got %q", cr.Nature)
	}
	if cr.Commands["reload"] != "nginx -t && {reload}" {
		t.Errorf("commands.reload not preserved: %v", cr.Commands)
	}
	if !cr.Sudo {
		t.Error("sudo should be true")
	}
}

// ── unknown nature error message ──────────────────────────────────────────────

func TestValidateYAML_unknownNature_errorMessage(t *testing.T) {
	msg := mustFail(t, `
scenarios:
  deploy:
    cues:
      - name: bin
        nature: bogus
`)
	if !strings.Contains(msg, `unknown nature "bogus"`) {
		t.Errorf("error should mention the unknown nature, got: %q", msg)
	}
}

func TestValidateYAML_unknownNature_includesScenarioAndCueName(t *testing.T) {
	msg := mustFail(t, `
scenarios:
  my-scenario:
    cues:
      - name: my-cue
        nature: typo
`)
	if !strings.Contains(msg, "my-scenario") {
		t.Errorf("error should include scenario name, got: %q", msg)
	}
	if !strings.Contains(msg, "my-cue") {
		t.Errorf("error should include cue name, got: %q", msg)
	}
	if !strings.Contains(msg, `"typo"`) {
		t.Errorf("error should include the bad nature value, got: %q", msg)
	}
}

// ── git: true ─────────────────────────────────────────────────────────────────

func TestValidateYAML_gitTrue_infersPack(t *testing.T) {
	cfg := mustLoad(t, `
scenarios:
  deploy:
    cues:
      - name: app
        git: true
        dest: /var/www/
`)
	cr := cfg.Scenarios["deploy"].Cues[0]
	if cr.Nature != "pack" {
		t.Errorf("want nature=pack inferred from git: true, got %q", cr.Nature)
	}
	if !cr.Git {
		t.Error("want Git=true on parsed cue")
	}
}

func TestValidateYAML_gitTrue_withSrc_error(t *testing.T) {
	msg := mustFail(t, `
scenarios:
  deploy:
    cues:
      - name: app
        git: true
        src: dist/**
        dest: /var/www/
`)
	if !strings.Contains(msg, "mutually exclusive") {
		t.Errorf("expected 'mutually exclusive' in error, got %q", msg)
	}
}

func TestValidateYAML_gitTrue_wrongNature_error(t *testing.T) {
	msg := mustFail(t, `
scenarios:
  deploy:
    cues:
      - name: app
        nature: config
        git: true
        dest: /var/www/
`)
	if !strings.Contains(msg, "nature: pack") {
		t.Errorf("expected 'nature: pack' in error, got %q", msg)
	}
}

// ── compensation: defer ──────────────────────────────────────────────────────

func TestValidateYAML_compensationDefer_valid(t *testing.T) {
	cfg := mustLoad(t, `
scenarios:
  deploy:
    cues:
      - name: install-deps
        shell: composer install
        compensation: defer
`)
	cr := cfg.Scenarios["deploy"].Cues[0]
	if cr.Compensation == nil || !cr.Compensation.Defer {
		t.Errorf("want Defer=true, got %+v", cr.Compensation)
	}
}

func TestValidateYAML_compensationDefer_noShell_error(t *testing.T) {
	msg := mustFail(t, `
scenarios:
  deploy:
    cues:
      - name: pack-cue
        nature: pack
        src: vendor/**
        dest: ./
        compensation: defer
`)
	if !strings.Contains(msg, "shell:") {
		t.Errorf("expected 'shell:' in error message, got %q", msg)
	}
}

// ── all known natures accepted via YAML ──────────────────────────────────────

func TestValidateYAML_allKnownNatures_parseWithoutError(t *testing.T) {
	// Verify every nature in KnownNatures parses and validates cleanly from YAML.
	// This is the regression guard: if a new nature is added to KnownNatures but
	// the YAML parsing or validation path rejects it, this test will catch it.
	for nature := range config.KnownNatures {
		nature := nature
		t.Run(nature, func(t *testing.T) {
			_, err := loadYAML(t, `
scenarios:
  s:
    cues:
      - name: x
        nature: `+nature+`
`)
			if err != nil {
				t.Errorf("nature %q should be valid via YAML, got: %v", nature, err)
			}
		})
	}
}
