// internal/cue/action_test.go
package cue_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
)

// mockConnAction embeds mockConn and overrides Run to return preset output/code.
type mockConnAction struct {
	mockConn
	runOutput string
	runCode   int
}

func (m *mockConnAction) Run(cmd string) (string, string, int, error) {
	return m.runOutput, "", m.runCode, nil
}

func (m *mockConnAction) RunWithEnv(cmd string, env map[string]string) (string, string, int, error) {
	return m.runOutput, "", m.runCode, nil
}

func TestActionExecutor_remote_changed(t *testing.T) {
	mock := &mockConnAction{runOutput: "git updated 3 files", runCode: 0}
	ex := cue.NewActionExecutor(mock)
	cr := config.CueRef{
		Name:        "pull",
		Nature:      "action",
		Shell:       "git fetch && git merge --ff-only HEAD",
		ChangedWhen: config.WhenExpr{Expression: "stdout contains updated"},
	}
	result, _ := ex.Execute(context.Background(), nil, cr, config.Target{})
	if result.Status != cue.StatusChanged {
		t.Errorf("want StatusChanged, got %v", result.Status)
	}
}

func TestActionExecutor_remote_equal(t *testing.T) {
	mock := &mockConnAction{runOutput: "Already up to date.", runCode: 0}
	ex := cue.NewActionExecutor(mock)
	cr := config.CueRef{
		Name:        "pull",
		Nature:      "action",
		Shell:       "git fetch && git merge --ff-only HEAD",
		ChangedWhen: config.WhenExpr{Expression: "stdout !contains Already up to date."},
	}
	result, _ := ex.Execute(context.Background(), nil, cr, config.Target{})
	if result.Status != cue.StatusEqual {
		t.Errorf("want StatusEqual, got %v", result.Status)
	}
}

func TestActionExecutor_local_changed(t *testing.T) {
	mock := &mockConnAction{}
	ex := cue.NewActionExecutor(mock)
	cr := config.CueRef{
		Name:   "ping",
		Nature: "action",
		Shell:  "echo hello",
		Local:  true,
	}
	result, err := ex.Execute(context.Background(), nil, cr, config.Target{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != cue.StatusChanged {
		t.Errorf("want StatusChanged, got %v", result.Status)
	}
}

func TestActionExecutor_failedWhen(t *testing.T) {
	mock := &mockConnAction{runOutput: "", runCode: 1}
	ex := cue.NewActionExecutor(mock)
	cr := config.CueRef{
		Name:       "check",
		Nature:     "action",
		Shell:      "systemctl is-active saver",
		FailedWhen: config.WhenExpr{Expression: "exit != 0"},
	}
	result, _ := ex.Execute(context.Background(), nil, cr, config.Target{})
	if result.Status != cue.StatusFailed {
		t.Errorf("want StatusFailed, got %v", result.Status)
	}
}

// mockConnSudo captures which Run variant was called.
type mockConnSudo struct {
	mockConn
	lastMethod string
	lastCmd    string
}

func (m *mockConnSudo) RunSudo(cmd string) (string, string, int, error) {
	m.lastMethod = "RunSudo"
	m.lastCmd = cmd
	return "", "", 0, nil
}

func (m *mockConnSudo) RunWithEnv(cmd string, _ map[string]string) (string, string, int, error) {
	m.lastMethod = "RunWithEnv"
	m.lastCmd = cmd
	return "", "", 0, nil
}

func TestActionExecutor_sudo_cue_flag(t *testing.T) {
	mock := &mockConnSudo{}
	ex := cue.NewActionExecutor(mock)
	cr := config.CueRef{
		Name:   "sendmail-disabled",
		Nature: "action",
		Shell:  "systemctl stop sendmail",
		Sudo:   true,
	}
	result, err := ex.Execute(context.Background(), mock, cr, config.Target{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status == cue.StatusFailed {
		t.Fatalf("unexpected failure: %v", result.Err)
	}
	if mock.lastMethod != "RunSudo" {
		t.Errorf("want RunSudo called, got %q", mock.lastMethod)
	}
}

func TestActionExecutor_sudo_target_flag(t *testing.T) {
	mock := &mockConnSudo{}
	ex := cue.NewActionExecutor(mock)
	cr := config.CueRef{
		Name:   "restart",
		Nature: "action",
		Shell:  "systemctl restart nginx",
	}
	result, err := ex.Execute(context.Background(), mock, cr, config.Target{Sudo: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status == cue.StatusFailed {
		t.Fatalf("unexpected failure: %v", result.Err)
	}
	if mock.lastMethod != "RunSudo" {
		t.Errorf("want RunSudo called for sudo target, got %q", mock.lastMethod)
	}
}

func TestActionExecutor_no_sudo_uses_RunWithEnv(t *testing.T) {
	mock := &mockConnSudo{}
	ex := cue.NewActionExecutor(mock)
	cr := config.CueRef{
		Name:   "check",
		Nature: "action",
		Shell:  "which nginx",
	}
	ex.Execute(context.Background(), mock, cr, config.Target{})
	if mock.lastMethod != "RunWithEnv" {
		t.Errorf("want RunWithEnv without sudo, got %q", mock.lastMethod)
	}
}

// TestActionExecutor_remote_cd_prefix verifies that a remote shell is prefixed with
// "cd '<dir>' && " so the command runs in target.Dir rather than the SSH home dir.
func TestActionExecutor_remote_cd_prefix(t *testing.T) {
	mock := &mockConnSudo{}
	ex := cue.NewActionExecutor(mock)
	cr := config.CueRef{Name: "restart", Nature: "action", Shell: "systemctl restart nginx"}
	ex.Execute(context.Background(), mock, cr, config.Target{Dir: "/app"})
	want := "cd '/app' && systemctl restart nginx"
	if mock.lastCmd != want {
		t.Errorf("remote cmd = %q, want %q", mock.lastCmd, want)
	}
}

// TestActionExecutor_remote_sudo_cd_prefix verifies the same cd prefix for the sudo path.
func TestActionExecutor_remote_sudo_cd_prefix(t *testing.T) {
	mock := &mockConnSudo{}
	ex := cue.NewActionExecutor(mock)
	cr := config.CueRef{Name: "restart", Nature: "action", Shell: "systemctl restart nginx", Sudo: true}
	ex.Execute(context.Background(), mock, cr, config.Target{Dir: "/app"})
	if mock.lastMethod != "RunSudo" {
		t.Fatalf("want RunSudo, got %q", mock.lastMethod)
	}
	want := "cd '/app' && systemctl restart nginx"
	if mock.lastCmd != want {
		t.Errorf("sudo cmd = %q, want %q", mock.lastCmd, want)
	}
}

// TestActionExecutor_remote_no_dir_no_cd_prefix verifies that when target.Dir is empty
// the shell is sent as-is (no spurious "cd  && " prepended).
func TestActionExecutor_remote_no_dir_no_cd_prefix(t *testing.T) {
	mock := &mockConnSudo{}
	ex := cue.NewActionExecutor(mock)
	cr := config.CueRef{Name: "check", Nature: "action", Shell: "which nginx"}
	ex.Execute(context.Background(), mock, cr, config.Target{})
	want := "which nginx"
	if mock.lastCmd != want {
		t.Errorf("cmd with empty Dir = %q, want %q", mock.lastCmd, want)
	}
}

// TestActionExecutor_local_uses_local_dir verifies that a local action shell runs
// with its CWD set to the directory stored in WithLocalDir (cfg.BaseDir).
func TestActionExecutor_local_uses_local_dir(t *testing.T) {
	dir := t.TempDir()
	ex := cue.NewActionExecutor(nil)
	cr := config.CueRef{Name: "pwd-check", Nature: "action", Shell: "pwd", Local: true}
	ctx := cue.WithLocalDir(context.Background(), dir)
	r, err := ex.Execute(ctx, nil, cr, config.Target{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status == cue.StatusFailed {
		t.Fatalf("unexpected failure: %v", r.Err)
	}
	// Resolve symlinks: macOS t.TempDir returns /var/… but pwd outputs /private/var/…
	want, _ := filepath.EvalSymlinks(dir)
	got, _ := filepath.EvalSymlinks(strings.TrimSpace(r.Stdout))
	if got != want {
		t.Errorf("local shell CWD = %q, want %q", got, want)
	}
}

func boolPtr(b bool) *bool { return &b }

func TestActionExecutor_changedWhen_boolLiteral_false(t *testing.T) {
	mock := &mockConnAction{runOutput: "done", runCode: 0}
	ex := cue.NewActionExecutor(mock)
	cr := config.CueRef{
		Name:        "deploy",
		Nature:      "action",
		Shell:       "make deploy",
		ChangedWhen: config.WhenExpr{BoolLiteral: boolPtr(false)},
	}
	r, err := ex.Execute(context.Background(), mock, cr, config.Target{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Status != cue.StatusEqual {
		t.Errorf("changed_when: false should yield StatusEqual, got %v", r.Status)
	}
}

func TestActionExecutor_changedWhen_boolLiteral_true(t *testing.T) {
	mock := &mockConnAction{runOutput: "done", runCode: 0}
	ex := cue.NewActionExecutor(mock)
	cr := config.CueRef{
		Name:        "deploy",
		Nature:      "action",
		Shell:       "make deploy",
		ChangedWhen: config.WhenExpr{BoolLiteral: boolPtr(true)},
	}
	r, err := ex.Execute(context.Background(), mock, cr, config.Target{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Status != cue.StatusChanged {
		t.Errorf("changed_when: true should yield StatusChanged, got %v", r.Status)
	}
}

func TestActionExecutor_changedWhen_shellProbe(t *testing.T) {
	mock := &mockConnAction{runOutput: "done", runCode: 0}
	ex := cue.NewActionExecutor(mock)
	cr := config.CueRef{
		Name:        "sync",
		Nature:      "action",
		Shell:       "make sync",
		ChangedWhen: config.WhenExpr{Shell: "true"}, // exit 0 = changed
	}
	r, err := ex.Execute(context.Background(), mock, cr, config.Target{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Status != cue.StatusChanged {
		t.Errorf("shell probe exit 0 should yield StatusChanged, got %v", r.Status)
	}
}
