// internal/cue/action_test.go
package cue_test

import (
	"context"
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
