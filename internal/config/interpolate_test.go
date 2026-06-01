// internal/config/interpolate_test.go
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"git.disroot.org/jmy/regis/internal/config"
)

func TestInterpolateString_shellEnv(t *testing.T) {
	os.Setenv("MY_HOST", "prod.example.com")
	defer os.Unsetenv("MY_HOST")
	got := config.InterpolateString("${MY_HOST}", nil)
	if got != "prod.example.com" {
		t.Errorf("want prod.example.com, got %q", got)
	}
}

func TestInterpolateString_shellOverridesDotenv(t *testing.T) {
	os.Setenv("MY_VAR", "from-shell")
	defer os.Unsetenv("MY_VAR")
	got := config.InterpolateString("${MY_VAR}", map[string]string{"MY_VAR": "from-file"})
	if got != "from-shell" {
		t.Errorf("shell env must win; got %q", got)
	}
}

func TestInterpolateString_dotenvFallback(t *testing.T) {
	os.Unsetenv("ONLY_IN_FILE")
	got := config.InterpolateString("${ONLY_IN_FILE}", map[string]string{"ONLY_IN_FILE": "file-value"})
	if got != "file-value" {
		t.Errorf("want file-value, got %q", got)
	}
}

func TestInterpolateString_unknown_unchanged(t *testing.T) {
	os.Unsetenv("MISSING_VAR")
	got := config.InterpolateString("${MISSING_VAR}", nil)
	if got != "${MISSING_VAR}" {
		t.Errorf("want placeholder unchanged, got %q", got)
	}
}

func TestInterpolateString_multiple(t *testing.T) {
	os.Setenv("HOST", "h")
	os.Setenv("USER", "u")
	defer os.Unsetenv("HOST")
	defer os.Unsetenv("USER")
	got := config.InterpolateString("${USER}@${HOST}", nil)
	if got != "u@h" {
		t.Errorf("want u@h, got %q", got)
	}
}

// TestInterpolateForTarget_AutoDiscovery: auto-discovers .env.prod for target "prod",
// host resolves to prod.example.com, NOT default.example.com from .env.local.
func TestInterpolateForTarget_AutoDiscovery(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "regis.yml", `
targets:
  - name: prod
    host: ${APP_HOST}
    user: deploy
    dir: /opt/app
scenarios: {}
`)
	writeFile(t, dir, ".env.local", "APP_HOST=default.example.com\n")
	writeFile(t, dir, ".env.prod", "APP_HOST=prod.example.com\n")

	cfg, err := config.LoadForTarget(filepath.Join(dir, "regis.yml"), "prod")
	if err != nil {
		t.Fatalf("LoadForTarget: %v", err)
	}
	if cfg.Targets[0].Host != "prod.example.com" {
		t.Errorf("want prod.example.com, got %q", cfg.Targets[0].Host)
	}
}

// TestInterpolateForTarget_ExplicitDotenv: t.Dotenv = ".env.override" takes precedence
// over auto-discovery.
func TestInterpolateForTarget_ExplicitDotenv(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "regis.yml", `
targets:
  - name: prod
    host: ${APP_HOST}
    user: deploy
    dir: /opt/app
    dotenv: .env.override
scenarios: {}
`)
	writeFile(t, dir, ".env.local", "APP_HOST=default.example.com\n")
	writeFile(t, dir, ".env.prod", "APP_HOST=prod.example.com\n")
	writeFile(t, dir, ".env.override", "APP_HOST=override.example.com\n")

	cfg, err := config.LoadForTarget(filepath.Join(dir, "regis.yml"), "prod")
	if err != nil {
		t.Fatalf("LoadForTarget: %v", err)
	}
	if cfg.Targets[0].Host != "override.example.com" {
		t.Errorf("want override.example.com, got %q", cfg.Targets[0].Host)
	}
}

// TestInterpolateForTarget_FallbackToLocal: no .env.prod file -> uses .env.local only.
func TestInterpolateForTarget_FallbackToLocal(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "regis.yml", `
targets:
  - name: prod
    host: ${APP_HOST}
    user: deploy
    dir: /opt/app
scenarios: {}
`)
	writeFile(t, dir, ".env.local", "APP_HOST=local.example.com\n")
	// no .env.prod file

	cfg, err := config.LoadForTarget(filepath.Join(dir, "regis.yml"), "prod")
	if err != nil {
		t.Fatalf("LoadForTarget: %v", err)
	}
	if cfg.Targets[0].Host != "local.example.com" {
		t.Errorf("want local.example.com, got %q", cfg.Targets[0].Host)
	}
}

// TestInterpolateForTarget_ShellEnvWins: shell env beats .env.prod.
func TestInterpolateForTarget_ShellEnvWins(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "regis.yml", `
targets:
  - name: prod
    host: ${APP_HOST}
    user: deploy
    dir: /opt/app
scenarios: {}
`)
	writeFile(t, dir, ".env.local", "APP_HOST=local.example.com\n")
	writeFile(t, dir, ".env.prod", "APP_HOST=prod.example.com\n")

	os.Setenv("APP_HOST", "shell.example.com")
	defer os.Unsetenv("APP_HOST")

	cfg, err := config.LoadForTarget(filepath.Join(dir, "regis.yml"), "prod")
	if err != nil {
		t.Fatalf("LoadForTarget: %v", err)
	}
	if cfg.Targets[0].Host != "shell.example.com" {
		t.Errorf("want shell.example.com, got %q", cfg.Targets[0].Host)
	}
}

// TestInterpolateForTarget_CueSrcResolvedPerTarget: cue src ${ENV_SERVER_FILE} resolves
// differently per target env file.
func TestInterpolateForTarget_CueSrcResolvedPerTarget(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "regis.yml", `
targets:
  - name: staging
    host: staging.example.com
    user: deploy
    dir: /opt/app
scenarios:
  deploy:
    cues:
      - name: bin
        nature: binary
        src: ${SERVER_BIN}
        dest: /usr/local/bin/app
`)
	writeFile(t, dir, ".env.local", "SERVER_BIN=app-local\n")
	writeFile(t, dir, ".env.staging", "SERVER_BIN=app-staging\n")

	cfg, err := config.LoadForTarget(filepath.Join(dir, "regis.yml"), "staging")
	if err != nil {
		t.Fatalf("LoadForTarget: %v", err)
	}
	sc := cfg.Scenarios["deploy"]
	if len(sc.Cues) == 0 {
		t.Fatal("no cues")
	}
	if sc.Cues[0].Src[0] != "app-staging" {
		t.Errorf("want app-staging, got %q", sc.Cues[0].Src[0])
	}
}

// TestLoadForTarget_TwoTargetsDifferentHosts: prod loads LoadForTarget("prod") -> prod host;
// staging loads LoadForTarget("staging") -> staging host.
func TestLoadForTarget_TwoTargetsDifferentHosts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "regis.yml", `
targets:
  - name: prod
    host: ${APP_HOST}
    user: deploy
    dir: /opt/app
  - name: staging
    host: ${APP_HOST}
    user: deploy
    dir: /opt/app
scenarios: {}
`)
	writeFile(t, dir, ".env.prod", "APP_HOST=prod.example.com\n")
	writeFile(t, dir, ".env.staging", "APP_HOST=staging.example.com\n")

	cfgProd, err := config.LoadForTarget(filepath.Join(dir, "regis.yml"), "prod")
	if err != nil {
		t.Fatalf("LoadForTarget prod: %v", err)
	}
	cfgStaging, err := config.LoadForTarget(filepath.Join(dir, "regis.yml"), "staging")
	if err != nil {
		t.Fatalf("LoadForTarget staging: %v", err)
	}

	var prodHost, stagingHost string
	for _, tgt := range cfgProd.Targets {
		if tgt.Name == "prod" {
			prodHost = tgt.Host
		}
	}
	for _, tgt := range cfgStaging.Targets {
		if tgt.Name == "staging" {
			stagingHost = tgt.Host
		}
	}

	if prodHost != "prod.example.com" {
		t.Errorf("prod host: want prod.example.com, got %q", prodHost)
	}
	if stagingHost != "staging.example.com" {
		t.Errorf("staging host: want staging.example.com, got %q", stagingHost)
	}
}

// TestInterpolateForTarget_Port_FromDotenv: port: ${NODE_PORT} is resolved from .env.prod.
func TestInterpolateForTarget_Port_FromDotenv(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "regis.yml", `
targets:
  - name: prod
    host: prod.example.com
    user: deploy
    port: ${NODE_PORT}
    dir: /opt/app
scenarios: {}
`)
	writeFile(t, dir, ".env.prod", "NODE_PORT=2222\n")

	cfg, err := config.LoadForTarget(filepath.Join(dir, "regis.yml"), "prod")
	if err != nil {
		t.Fatalf("LoadForTarget: %v", err)
	}
	if cfg.Targets[0].Port != "2222" {
		t.Errorf("want port \"2222\", got %q", cfg.Targets[0].Port)
	}
}

// TestInterpolateForTarget_Port_LiteralInteger: port: 22 (integer YAML) loads as "22".
func TestInterpolateForTarget_Port_LiteralInteger(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "regis.yml", `
targets:
  - name: prod
    host: prod.example.com
    user: deploy
    port: 22
    dir: /opt/app
scenarios: {}
`)

	cfg, err := config.LoadForTarget(filepath.Join(dir, "regis.yml"), "prod")
	if err != nil {
		t.Fatalf("LoadForTarget: %v", err)
	}
	if cfg.Targets[0].Port != "22" {
		t.Errorf("want port \"22\", got %q", cfg.Targets[0].Port)
	}
}

// TestInterpolateForTarget_Port_ShellEnvWins: shell env beats .env.prod for port.
func TestInterpolateForTarget_Port_ShellEnvWins(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "regis.yml", `
targets:
  - name: prod
    host: prod.example.com
    user: deploy
    port: ${NODE_PORT}
    dir: /opt/app
scenarios: {}
`)
	writeFile(t, dir, ".env.prod", "NODE_PORT=2222\n")
	os.Setenv("NODE_PORT", "3333")
	defer os.Unsetenv("NODE_PORT")

	cfg, err := config.LoadForTarget(filepath.Join(dir, "regis.yml"), "prod")
	if err != nil {
		t.Fatalf("LoadForTarget: %v", err)
	}
	if cfg.Targets[0].Port != "3333" {
		t.Errorf("shell env must win; want \"3333\", got %q", cfg.Targets[0].Port)
	}
}

// TestInterpolateForTarget_NilTarget: nil target -> .env.local only, applies to all targets
// (backward compat).
func TestInterpolateForTarget_NilTarget(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "regis.yml", `
targets:
  - name: prod
    host: ${APP_HOST}
    user: deploy
    dir: /opt/app
  - name: staging
    host: ${APP_HOST}
    user: deploy
    dir: /opt/app
scenarios: {}
`)
	writeFile(t, dir, ".env.local", "APP_HOST=local.example.com\n")
	// no per-target .env files

	cfg, err := config.Load(filepath.Join(dir, "regis.yml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, tgt := range cfg.Targets {
		if tgt.Host != "local.example.com" {
			t.Errorf("target %s: want local.example.com, got %q", tgt.Name, tgt.Host)
		}
	}
}
