// internal/config/loader_test.go
package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"git.disroot.org/jmy/regis/internal/config"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLoad_minimal(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "regis.yml", `
targets:
  - name: prod
    host: prod.example.com
    user: deploy
    port: 22
    dir: /opt/app
scenarios:
  build:
    describe: "Build"
    cues:
      - name: bins
        nature: action
        local: true
        cmd: go build ./...
`)
	cfg, err := config.Load(filepath.Join(dir, "regis.yml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Targets) != 1 || cfg.Targets[0].Name != "prod" {
		t.Errorf("targets: %+v", cfg.Targets)
	}
	s, ok := cfg.Scenarios["build"]
	if !ok {
		t.Fatal("scenario 'build' not found")
	}
	if len(s.Cues) != 1 || s.Cues[0].Shell != "go build ./..." {
		t.Errorf("cues: %+v", s.Cues)
	}
}

func TestLoad_fileNotFound(t *testing.T) {
	_, err := config.Load("/nonexistent/regis.yml")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoad_autoDiscover(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "regis.yml", `
targets:
  - name: prod
    host: h
    user: u
    dir: /opt/app
scenarios:
  build:
    cues:
      - name: bins
        nature: action
        local: true
        cmd: go build ./...
`)
	writeFile(t, dir, "regis.mailway.yml", `
scenarios:
  mailway:
    describe: "SMTP relay"
    cues:
      - name: bin
        nature: binary
        src: bin/mailway
        dest: mailway
`)
	cfg, err := config.Load(filepath.Join(dir, "regis.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Scenarios["mailway"]; !ok {
		t.Error("auto-discovered scenario 'mailway' not merged")
	}
}

func TestLoad_duplicateScenarioError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "regis.yml", `
targets:
  - name: prod
    host: h
    user: u
    dir: /opt/app
scenarios:
  build:
    cues: []
`)
	writeFile(t, dir, "regis.extra.yml", `
scenarios:
  build:
    cues: []
`)
	_, err := config.Load(filepath.Join(dir, "regis.yml"))
	if err == nil {
		t.Error("expected error for duplicate scenario 'build'")
	}
}

func TestLoad_explicitInclude(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "regis.yml", `
targets:
  - name: prod
    host: h
    user: u
    dir: /opt/app
includes:
  - extra.yml
scenarios:
  build:
    cues: []
`)
	writeFile(t, dir, "extra.yml", `
scenarios:
  deploy:
    cues: []
`)
	cfg, err := config.Load(filepath.Join(dir, "regis.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Scenarios["deploy"]; !ok {
		t.Error("explicit include 'deploy' not merged")
	}
}

func TestLoad_defaultsEnv_firstFileWins(t *testing.T) {
	dir := t.TempDir()
	// Primary file defines KEY_A and KEY_B.
	writeFile(t, dir, "regis.yml", `
targets:
  - name: prod
    host: h
    user: u
    dir: /opt/app
defaults:
  env:
    KEY_A: "primary"
    KEY_B: "primary"
scenarios: {}
`)
	// Extra file defines KEY_B (conflict) and KEY_C (new).
	writeFile(t, dir, "regis.extra.yml", `
defaults:
  env:
    KEY_B: "extra"
    KEY_C: "extra"
scenarios: {}
`)
	cfg, err := config.Load(filepath.Join(dir, "regis.yml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Defaults.Env["KEY_A"] != "primary" {
		t.Errorf("KEY_A: want primary, got %q", cfg.Defaults.Env["KEY_A"])
	}
	// First file wins on conflict.
	if cfg.Defaults.Env["KEY_B"] != "primary" {
		t.Errorf("KEY_B: want primary (first-file-wins), got %q", cfg.Defaults.Env["KEY_B"])
	}
	// Key only in extra file is added.
	if cfg.Defaults.Env["KEY_C"] != "extra" {
		t.Errorf("KEY_C: want extra, got %q", cfg.Defaults.Env["KEY_C"])
	}
}

func TestLoad_explicitInclude_dedup(t *testing.T) {
	dir := t.TempDir()
	// regis.extra.yml is both auto-discovered AND listed in includes:
	writeFile(t, dir, "regis.yml", `
targets:
  - name: prod
    host: h
    user: u
    dir: /opt/app
includes:
  - regis.extra.yml
scenarios:
  build:
    cues: []
`)
	writeFile(t, dir, "regis.extra.yml", `
scenarios:
  deploy:
    cues: []
`)
	cfg, err := config.Load(filepath.Join(dir, "regis.yml"))
	if err != nil {
		t.Fatalf("expected no error for double-listed file, got: %v", err)
	}
	if _, ok := cfg.Scenarios["deploy"]; !ok {
		t.Error("scenario 'deploy' from extra file not present")
	}
}
