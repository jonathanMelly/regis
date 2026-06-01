// internal/config/yaml_test.go
package config_test

import (
	"testing"
	"gopkg.in/yaml.v3"
	"git.disroot.org/jmy/regis/internal/config"
)

func TestStringOrList_scalar(t *testing.T) {
	var got config.StringOrList
	if err := yaml.Unmarshal([]byte(`"build"`), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "build" {
		t.Errorf("want [build], got %v", got)
	}
}

func TestStringOrList_sequence(t *testing.T) {
	var got config.StringOrList
	if err := yaml.Unmarshal([]byte(`[build, checks]`), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "build" || got[1] != "checks" {
		t.Errorf("want [build checks], got %v", got)
	}
}

func TestPostAction_string(t *testing.T) {
	var got config.PostAction
	if err := yaml.Unmarshal([]byte(`"restart:saver"`), &got); err != nil {
		t.Fatal(err)
	}
	if got.Cmd != "restart:saver" {
		t.Errorf("want restart:saver, got %q", got.Cmd)
	}
}

func TestPostAction_object(t *testing.T) {
	var got config.PostAction
	if err := yaml.Unmarshal([]byte("cmd: nginx -t && nginx -s reload\nsudo: true"), &got); err != nil {
		t.Fatal(err)
	}
	if got.Cmd != "nginx -t && nginx -s reload" || !got.Sudo {
		t.Errorf("unexpected: %+v", got)
	}
}

func TestWhenExpr_expression(t *testing.T) {
	var got config.WhenExpr
	if err := yaml.Unmarshal([]byte(`"stdout contains Updated"`), &got); err != nil {
		t.Fatal(err)
	}
	if got.Expression != "stdout contains Updated" {
		t.Errorf("want expression, got %+v", got)
	}
}

func TestWhenExpr_bool_false(t *testing.T) {
	var got config.WhenExpr
	if err := yaml.Unmarshal([]byte(`false`), &got); err != nil {
		t.Fatal(err)
	}
	if got.BoolLiteral == nil || *got.BoolLiteral != false {
		t.Errorf("want false, got %+v", got)
	}
}

func TestWhenExpr_shell(t *testing.T) {
	var got config.WhenExpr
	if err := yaml.Unmarshal([]byte("shell: \"test -f /tmp/done\""), &got); err != nil {
		t.Fatal(err)
	}
	if got.Shell != "test -f /tmp/done" {
		t.Errorf("want shell probe, got %+v", got)
	}
}

func TestScenario_needs_alias(t *testing.T) {
	const src = `
describe: "My scenario"
needs: [build, checks]
cues: []
`
	var got config.Scenario
	if err := yaml.Unmarshal([]byte(src), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Requires) != 2 {
		t.Errorf("needs: alias not respected, Requires=%v", got.Requires)
	}
}

func TestCueRef_cmd_alias(t *testing.T) {
	const src = `
name: build
nature: action
local: true
cmd: go build ./...
`
	var got config.CueRef
	if err := yaml.Unmarshal([]byte(src), &got); err != nil {
		t.Fatal(err)
	}
	if got.Shell != "go build ./..." {
		t.Errorf("cmd: alias not mapped to Shell, got %q", got.Shell)
	}
}

func TestCueRef_shell_and_cmd_error(t *testing.T) {
	const src = `
name: build
shell: go build ./...
cmd: go build ./...
`
	var got config.CueRef
	if err := yaml.Unmarshal([]byte(src), &got); err == nil {
		t.Error("expected error for both shell: and cmd:")
	}
}

func TestWhenExpr_scenario_cue(t *testing.T) {
	var got config.WhenExpr
	if err := yaml.Unmarshal([]byte("scenario: health-check\ncue: ping"), &got); err != nil {
		t.Fatal(err)
	}
	if got.ScenarioRef != "health-check" || got.CueRef != "ping" {
		t.Errorf("unexpected: %+v", got)
	}
}

func TestWhenExpr_shell_and_scenario_error(t *testing.T) {
	var got config.WhenExpr
	if err := yaml.Unmarshal([]byte("shell: test -f /tmp/ok\nscenario: health"), &got); err == nil {
		t.Error("expected error for both shell: and scenario:")
	}
}

func TestWhenExpr_cue_without_scenario_error(t *testing.T) {
	var got config.WhenExpr
	if err := yaml.Unmarshal([]byte("cue: ping"), &got); err == nil {
		t.Error("expected error for cue: without scenario:")
	}
}

func TestTarget_port_integer(t *testing.T) {
	const src = `
name: prod
host: 192.0.2.1
user: deploy
port: 2222
dir: /srv
`
	var got config.Target
	if err := yaml.Unmarshal([]byte(src), &got); err != nil {
		t.Fatal(err)
	}
	if got.Port != "2222" {
		t.Errorf("want port \"2222\", got %q", got.Port)
	}
}

func TestTarget_port_string_var(t *testing.T) {
	const src = `
name: prod
host: 192.0.2.1
user: deploy
port: ${NODE_PORT}
dir: /srv
`
	var got config.Target
	if err := yaml.Unmarshal([]byte(src), &got); err != nil {
		t.Fatalf("port: ${VAR} should parse without error: %v", err)
	}
	if got.Port != "${NODE_PORT}" {
		t.Errorf("want port \"${NODE_PORT}\", got %q", got.Port)
	}
}

func TestTarget_port_absent(t *testing.T) {
	const src = `
name: prod
host: 192.0.2.1
user: deploy
dir: /srv
`
	var got config.Target
	if err := yaml.Unmarshal([]byte(src), &got); err != nil {
		t.Fatal(err)
	}
	if got.Port != "" {
		t.Errorf("want empty port when absent, got %q", got.Port)
	}
}

func TestPrePost_object(t *testing.T) {
	var got config.PrePost
	if err := yaml.Unmarshal([]byte("cmd: make test\nif: \"[ -f Makefile ]\""), &got); err != nil {
		t.Fatal(err)
	}
	if got.Cmd != "make test" || got.If != "[ -f Makefile ]" {
		t.Errorf("unexpected: %+v", got)
	}
}

func TestPrePost_local(t *testing.T) {
	var got config.PrePost
	if err := yaml.Unmarshal([]byte("cmd: \"curl https://example.com\"\nlocal: true"), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Local {
		t.Errorf("want Local=true, got false")
	}
	if got.Cmd != "curl https://example.com" {
		t.Errorf("unexpected cmd: %q", got.Cmd)
	}
}

func TestPrePost_scalar_local_defaults_false(t *testing.T) {
	var got config.PrePost
	if err := yaml.Unmarshal([]byte("\"systemctl stop svc\""), &got); err != nil {
		t.Fatal(err)
	}
	if got.Local {
		t.Errorf("scalar form should default local=false, got true")
	}
}

func TestScenario_env(t *testing.T) {
	const src = `
describe: "Build"
env:
  CGO_ENABLED: "0"
  GOOS: linux
cues: []
`
	var got config.Scenario
	if err := yaml.Unmarshal([]byte(src), &got); err != nil {
		t.Fatal(err)
	}
	if got.Env["CGO_ENABLED"] != "0" || got.Env["GOOS"] != "linux" {
		t.Errorf("Scenario.Env not parsed: %v", got.Env)
	}
}

func TestConfig_defaultsEnv(t *testing.T) {
	const src = `
defaults:
  env:
    CGO_ENABLED: "0"
    GOOS: linux
scenarios: {}
`
	var got config.Config
	if err := yaml.Unmarshal([]byte(src), &got); err != nil {
		t.Fatal(err)
	}
	if got.Defaults.Env["CGO_ENABLED"] != "0" || got.Defaults.Env["GOOS"] != "linux" {
		t.Errorf("Config.Defaults.Env not parsed: %v", got.Defaults.Env)
	}
}

// ── CueRollback.UnmarshalYAML ─────────────────────────────────────────────

func TestCueRollback_boolTrue(t *testing.T) {
	var got config.CueRollback
	if err := yaml.Unmarshal([]byte(`true`), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || got.Shell != "" || got.Sudo {
		t.Errorf("rollback: true — unexpected: %+v", got)
	}
}

func TestCueRollback_boolFalse(t *testing.T) {
	var got config.CueRollback
	if err := yaml.Unmarshal([]byte(`false`), &got); err != nil {
		t.Fatal(err)
	}
	if got.Enabled {
		t.Errorf("rollback: false — expected Enabled=false, got %+v", got)
	}
}

func TestCueRollback_stringShell(t *testing.T) {
	var got config.CueRollback
	if err := yaml.Unmarshal([]byte(`"rm -f maintenance.flag"`), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || got.Shell != "rm -f maintenance.flag" || got.Sudo {
		t.Errorf("rollback: string — unexpected: %+v", got)
	}
}

func TestCueRollback_objectForm(t *testing.T) {
	var got config.CueRollback
	if err := yaml.Unmarshal([]byte("shell: systemctl stop myapp\nsudo: true"), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || got.Shell != "systemctl stop myapp" || !got.Sudo {
		t.Errorf("rollback: {shell, sudo} — unexpected: %+v", got)
	}
}

func TestCueRef_rollbackParsedFromYAML(t *testing.T) {
	const src = `
name: go-offline
nature: action
shell: touch maintenance.flag
rollback: "rm -f maintenance.flag"
`
	var got config.CueRef
	if err := yaml.Unmarshal([]byte(src), &got); err != nil {
		t.Fatal(err)
	}
	if got.Rollback == nil || !got.Rollback.Enabled || got.Rollback.Shell != "rm -f maintenance.flag" {
		t.Errorf("CueRef.Rollback not parsed: %+v", got.Rollback)
	}
}

func TestCueRef_rollbackTrueForFileNature(t *testing.T) {
	const src = `
name: frontend
nature: pack
src: dist/**
dest: /var/www/
rollback: true
`
	var got config.CueRef
	if err := yaml.Unmarshal([]byte(src), &got); err != nil {
		t.Fatal(err)
	}
	if got.Rollback == nil || !got.Rollback.Enabled || got.Rollback.Shell != "" {
		t.Errorf("CueRef.Rollback (true form) not parsed: %+v", got.Rollback)
	}
}
