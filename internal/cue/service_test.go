// internal/cue/service_test.go
package cue_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
)

// runCode returns a runFunc that always exits with the given code.
func runCode(code int) func(string) (string, string, int, error) {
	return func(string) (string, string, int, error) { return "", "", code, nil }
}

// runCapture returns a runFunc that exits with code 0 and appends each command to *cmds.
func runCapture(cmds *[]string, code int) func(string) (string, string, int, error) {
	return func(cmd string) (string, string, int, error) {
		*cmds = append(*cmds, cmd)
		return "", "", code, nil
	}
}

// ── systemd, no service_file ─────────────────────────────────────────────────

func TestServiceExecutor_systemd_noFile_enabled(t *testing.T) {
	mock := &mockConn{runFunc: runCode(0)} // is-enabled exits 0
	r, err := cue.NewServiceExecutor(mock).Execute(
		context.Background(), nil,
		config.CueRef{Name: "mailway", Nature: "service", Manager: "systemd"},
		config.Target{Dir: "/opt/app"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != cue.StatusEqual {
		t.Errorf("want StatusEqual (enabled), got %v", r.Status)
	}
	if r.Cmd != "systemctl enable mailway" {
		t.Errorf("systemd no-file Cmd label: want %q, got %q", "systemctl enable mailway", r.Cmd)
	}
}

func TestServiceExecutor_systemd_noFile_notEnabled(t *testing.T) {
	mock := &mockConn{runFunc: runCode(1)} // is-enabled exits 1
	r, err := cue.NewServiceExecutor(mock).Execute(
		cue.WithCheckOnly(context.Background()), nil,
		config.CueRef{Name: "mailway", Nature: "service", Manager: "systemd"},
		config.Target{Dir: "/opt/app"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != cue.StatusChanged {
		t.Errorf("want StatusChanged (not enabled), got %v", r.Status)
	}
	if len(r.PostActions) != 0 {
		t.Error("dry-run must not queue post-actions")
	}
}

func TestServiceExecutor_systemd_noFile_notEnabled_isEnabledCommand(t *testing.T) {
	var cmds []string
	mock := &mockConn{runFunc: runCapture(&cmds, 1)}
	cue.NewServiceExecutor(mock).Execute( //nolint
		cue.WithCheckOnly(context.Background()), nil,
		config.CueRef{Name: "mailway", Nature: "service", Manager: "systemd"},
		config.Target{Dir: "/opt/app"},
	)
	if len(cmds) == 0 || !strings.Contains(cmds[0], "is-enabled") || !strings.Contains(cmds[0], "mailway") {
		t.Errorf("expected systemctl is-enabled mailway command, got %v", cmds)
	}
}

// ── systemd, with service_file ────────────────────────────────────────────────

func TestServiceExecutor_systemd_fileUnchanged_enabled(t *testing.T) {
	content := []byte("[Unit]\nDescription=mailway\n")
	dir := t.TempDir()
	localPath := dir + "/mailway.service"
	os.WriteFile(localPath, content, 0644)

	mock := &mockConn{
		runFunc: runCode(0),
		downloads: map[string][]byte{
			"/etc/systemd/system/mailway.service": content,
		},
	}
	r, err := cue.NewServiceExecutor(mock).Execute(
		context.Background(), nil,
		config.CueRef{Name: "mailway", Nature: "service", Manager: "systemd", ServiceFile: localPath},
		config.Target{Dir: "/opt/app"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != cue.StatusEqual {
		t.Errorf("want StatusEqual (file same, enabled), got %v", r.Status)
	}
}

func TestServiceExecutor_systemd_fileChanged_enabled(t *testing.T) {
	dir := t.TempDir()
	localPath := dir + "/mailway.service"
	os.WriteFile(localPath, []byte("[Unit]\nDescription=mailway v2\n"), 0644)

	mock := &mockConn{
		runFunc: runCode(0),
		downloads: map[string][]byte{
			"/etc/systemd/system/mailway.service": []byte("[Unit]\nDescription=mailway v1\n"),
		},
	}
	r, err := cue.NewServiceExecutor(mock).Execute(
		context.Background(), nil,
		config.CueRef{Name: "mailway", Nature: "service", Manager: "systemd", ServiceFile: localPath, Sudo: true},
		config.Target{Dir: "/opt/app"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != cue.StatusChanged {
		t.Errorf("want StatusChanged (file differs), got %v", r.Status)
	}
	if mock.uploadPath != "/etc/systemd/system/mailway.service" {
		t.Errorf("expected upload to unit path, got %q", mock.uploadPath)
	}
	if r.Cmd != "/etc/systemd/system/mailway.service" {
		t.Errorf("systemd with service_file Cmd label: want %q, got %q", "/etc/systemd/system/mailway.service", r.Cmd)
	}
	if len(r.PostActions) == 0 || !strings.HasPrefix(r.PostActions[0].Cmd, "deploy:") {
		t.Errorf("expected deploy: post-action, got %v", r.PostActions)
	}
	if !r.PostActions[0].Sudo {
		t.Error("post-action sudo should inherit from cue.Sudo")
	}
	if r.Diff == "" {
		t.Error("diff should be non-empty when file changed")
	}
}

func TestServiceExecutor_systemd_fileUnchanged_notEnabled(t *testing.T) {
	// File is same but service not enabled → still StatusChanged
	content := []byte("[Unit]\nDescription=mailway\n")
	dir := t.TempDir()
	localPath := dir + "/mailway.service"
	os.WriteFile(localPath, content, 0644)

	mock := &mockConn{
		runFunc: runCode(1), // is-enabled fails
		downloads: map[string][]byte{
			"/etc/systemd/system/mailway.service": content,
		},
	}
	r, err := cue.NewServiceExecutor(mock).Execute(
		cue.WithCheckOnly(context.Background()), nil,
		config.CueRef{Name: "mailway", Nature: "service", Manager: "systemd", ServiceFile: localPath},
		config.Target{Dir: "/opt/app"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != cue.StatusChanged {
		t.Errorf("want StatusChanged (not enabled), got %v", r.Status)
	}
}

func TestServiceExecutor_systemd_dryRun_noUpload(t *testing.T) {
	dir := t.TempDir()
	localPath := dir + "/mailway.service"
	os.WriteFile(localPath, []byte("[Unit]\nDescription=new\n"), 0644)

	mock := &mockConn{
		runFunc: runCode(0),
		downloads: map[string][]byte{
			"/etc/systemd/system/mailway.service": []byte("[Unit]\nDescription=old\n"),
		},
	}
	r, err := cue.NewServiceExecutor(mock).Execute(
		cue.WithCheckOnly(context.Background()), nil,
		config.CueRef{Name: "mailway", Nature: "service", Manager: "systemd", ServiceFile: localPath},
		config.Target{Dir: "/opt/app"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != cue.StatusChanged {
		t.Errorf("want StatusChanged in dry-run, got %v", r.Status)
	}
	if mock.uploaded != nil {
		t.Error("dry-run must not upload")
	}
	if len(r.PostActions) != 0 {
		t.Error("dry-run must not queue post-actions")
	}
}

func TestServiceExecutor_systemd_fileReadError(t *testing.T) {
	mock := &mockConn{runFunc: runCode(0)}
	r, err := cue.NewServiceExecutor(mock).Execute(
		context.Background(), nil,
		config.CueRef{Name: "mailway", Nature: "service", Manager: "systemd",
			ServiceFile: "/nonexistent/mailway.service"},
		config.Target{Dir: "/opt/app"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != cue.StatusFailed {
		t.Errorf("want StatusFailed (file unreadable), got %v", r.Status)
	}
}

// ── crontab ───────────────────────────────────────────────────────────────────

func TestServiceExecutor_crontab_installed(t *testing.T) {
	mock := &mockConn{runFunc: runCode(0)} // grep finds entry
	r, err := cue.NewServiceExecutor(mock).Execute(
		context.Background(), nil,
		config.CueRef{Name: "saver", Nature: "service", Manager: "crontab", Binary: "saver"},
		config.Target{Dir: "/opt/app"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != cue.StatusEqual {
		t.Errorf("want StatusEqual (crontab entry present), got %v", r.Status)
	}
	if r.Cmd != "crontab: saver" {
		t.Errorf("crontab Cmd label: want %q, got %q", "crontab: saver", r.Cmd)
	}
}

func TestServiceExecutor_crontab_notInstalled_dryRun(t *testing.T) {
	mock := &mockConn{runFunc: runCode(1)} // grep returns 1 (not found)
	r, err := cue.NewServiceExecutor(mock).Execute(
		cue.WithCheckOnly(context.Background()), nil,
		config.CueRef{Name: "saver", Nature: "service", Manager: "crontab", Binary: "saver"},
		config.Target{Dir: "/opt/app"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != cue.StatusChanged {
		t.Errorf("want StatusChanged (crontab entry absent), got %v", r.Status)
	}
}

func TestServiceExecutor_crontab_notInstalled_realRun(t *testing.T) {
	mock := &mockConn{runFunc: runCode(1)}
	r, err := cue.NewServiceExecutor(mock).Execute(
		context.Background(), nil,
		config.CueRef{Name: "saver", Nature: "service", Manager: "crontab", Binary: "saver"},
		config.Target{Dir: "/opt/app"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != cue.StatusChanged {
		t.Errorf("want StatusChanged, got %v", r.Status)
	}
	if len(r.PostActions) == 0 || r.PostActions[0].Cmd != "deploy:saver" {
		t.Errorf("expected deploy:saver post-action, got %v", r.PostActions)
	}
}

func TestServiceExecutor_crontab_grepIncludesPath(t *testing.T) {
	// Verify the grep command includes the full dir/binary path.
	var cmds []string
	mock := &mockConn{runFunc: runCapture(&cmds, 0)}
	cue.NewServiceExecutor(mock).Execute( //nolint
		context.Background(), nil,
		config.CueRef{Name: "saver", Nature: "service", Manager: "crontab", Binary: "saver"},
		config.Target{Dir: "/opt/app"},
	)
	if len(cmds) == 0 || !strings.Contains(cmds[0], "/opt/app/saver") {
		t.Errorf("crontab check should grep for /opt/app/saver, got %v", cmds)
	}
}

// ── __REMOTE_DIR__ template expansion ────────────────────────────────────────

func TestServiceExecutor_targetDir_expandedBeforeDiff(t *testing.T) {
	// Local template uses ${TARGET_DIR}; after expansion it matches the remote → StatusEqual.
	dir := t.TempDir()
	localPath := dir + "/svc.service"
	os.WriteFile(localPath, []byte("ExecStart=${TARGET_DIR}/svc\n"), 0644)

	mock := &mockConn{
		runFunc: runCode(0), // is-enabled = ok
		downloads: map[string][]byte{
			"/etc/systemd/system/svc.service": []byte("ExecStart=/opt/app/svc\n"),
		},
	}
	env := map[string]string{"TARGET_DIR": "/opt/app"}
	r, err := cue.NewServiceExecutor(mock, env).Execute(
		context.Background(), nil,
		config.CueRef{Name: "svc", Nature: "service", Manager: "systemd", ServiceFile: localPath},
		config.Target{Dir: "/opt/app"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != cue.StatusEqual {
		t.Errorf("want StatusEqual (template expands to match remote), got %v\ndiff:\n%s", r.Status, r.Diff)
	}
}

func TestServiceExecutor_targetDir_uploadRendered(t *testing.T) {
	// Verify uploaded bytes contain the expanded path, not the raw ${TARGET_DIR} placeholder.
	dir := t.TempDir()
	localPath := dir + "/svc.service"
	os.WriteFile(localPath, []byte("ExecStart=${TARGET_DIR}/svc\n"), 0644)

	mock := &mockConn{
		runFunc:   runCode(0),
		downloads: map[string][]byte{"/etc/systemd/system/svc.service": []byte("ExecStart=/opt/app/old\n")},
	}
	env := map[string]string{"TARGET_DIR": "/opt/app"}
	_, err := cue.NewServiceExecutor(mock, env).Execute(
		context.Background(), nil,
		config.CueRef{Name: "svc", Nature: "service", Manager: "systemd", ServiceFile: localPath},
		config.Target{Dir: "/opt/app"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mock.uploaded), "${TARGET_DIR}") {
		t.Error("uploaded content must not contain ${TARGET_DIR} placeholder")
	}
	if !strings.Contains(string(mock.uploaded), "/opt/app/svc") {
		t.Errorf("uploaded content should contain expanded path, got: %q", string(mock.uploaded))
	}
}

func TestServiceExecutor_diffHeaders_fullPaths(t *testing.T) {
	dir := t.TempDir()
	localPath := dir + "/svc.service"
	os.WriteFile(localPath, []byte("ExecStart=/opt/app/new\n"), 0644)

	mock := &mockConn{
		runFunc:   runCode(0),
		downloads: map[string][]byte{"/etc/systemd/system/svc.service": []byte("ExecStart=/opt/app/old\n")},
	}
	r, err := cue.NewServiceExecutor(mock).Execute(
		cue.WithCheckOnly(context.Background()), nil,
		config.CueRef{Name: "svc", Nature: "service", Manager: "systemd", ServiceFile: localPath},
		config.Target{Dir: "/opt/app"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Diff, "/etc/systemd/system/svc.service") {
		t.Errorf("diff header should contain full remote path, got:\n%s", r.Diff)
	}
	if !strings.Contains(r.Diff, localPath) {
		t.Errorf("diff header should contain full local path, got:\n%s", r.Diff)
	}
}

// ── custom manager ────────────────────────────────────────────────────────────

func TestServiceExecutor_customManager_alwaysEqual(t *testing.T) {
	// runFunc always fails (code 1) but should never be called for custom managers.
	var cmds []string
	mock := &mockConn{runFunc: runCapture(&cmds, 1)}
	r, err := cue.NewServiceExecutor(mock).Execute(
		context.Background(), nil,
		config.CueRef{Name: "api", Nature: "service", Manager: "pm2"},
		config.Target{Dir: "/opt/app"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != cue.StatusEqual {
		t.Errorf("custom manager: want StatusEqual (no check), got %v", r.Status)
	}
	if len(cmds) != 0 {
		t.Errorf("custom manager: must not run any remote commands, got %v", cmds)
	}
}
