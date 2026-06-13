// internal/cue/render_test.go
package cue_test

import (
	"context"
	"crypto/md5"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
)

// singleFileFixture pre-writes content to a temp file and returns (noOpShell, localDestPath).
// Tests set LocalDest to localDestPath; the no-op shell leaves the pre-written content intact.
// This avoids shell-level file-write commands that differ between sh, cmd.exe and PowerShell.
func singleFileFixture(t *testing.T, content string) (shell, localDest string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "artifact")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return noOpShell(), p
}

// folderFixture pre-writes files into a temp dir and returns (noOpShell, localDestPath).
func folderFixture(t *testing.T, files map[string]string) (shell, localDest string) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return noOpShell(), dir + "/"
}

// noOpShell returns a cross-platform shell command that exits 0 and writes nothing.
func noOpShell() string {
	if runtime.GOOS == "windows" {
		return "rem" // cmd.exe remark — always exits 0
	}
	return "true"
}

// mockRenderConn supports per-path Download, Run (find/rm), and upload tracking.
type mockRenderConn struct {
	mockConn
	remoteFiles   map[string][]byte // path → content for Download
	runOutput     string            // returned by Run for "find ..." commands
	deletedPaths  []string          // paths "deleted" via "rm -f" Run calls
	uploadedPaths []string          // all paths uploaded via UploadBytes/Upload
}

func (m *mockRenderConn) Download(path string) ([]byte, error) {
	if data, ok := m.remoteFiles[path]; ok {
		return data, nil
	}
	return nil, fmt.Errorf("not found: %s", path)
}

func (m *mockRenderConn) Run(cmd string) (string, string, int, error) {
	if strings.HasPrefix(cmd, "find ") {
		return m.runOutput, "", 0, nil
	}
	if strings.HasPrefix(cmd, "rm -f ") {
		path := strings.TrimSpace(strings.TrimPrefix(cmd, "rm -f "))
		path = strings.Trim(path, "'")
		m.deletedPaths = append(m.deletedPaths, path)
		return "", "", 0, nil
	}
	// md5sum / md5 -q: return hashes for files in remoteFiles.
	if strings.Contains(cmd, "md5sum") || strings.Contains(cmd, "md5 -q") {
		var sb strings.Builder
		for path, data := range m.remoteFiles {
			if strings.Contains(cmd, "'"+path+"'") || strings.Contains(cmd, path) {
				h := md5.Sum(data)
				fmt.Fprintf(&sb, "%x  %s\n", h, path)
			}
		}
		return sb.String(), "", 0, nil
	}
	return "", "", 0, nil
}

func (m *mockRenderConn) UploadBytes(data []byte, remote string, _ fs.FileMode, _ bool) error {
	m.uploadedPaths = append(m.uploadedPaths, remote)
	return m.mockConn.uploadErr
}

func (m *mockRenderConn) Upload(local, remote string, _ fs.FileMode, _ bool) error {
	m.uploadedPaths = append(m.uploadedPaths, remote)
	return m.mockConn.uploadErr
}

// --- single file tests ---

func TestRenderExecutor_single_equal(t *testing.T) {
	content := "server { listen 80; }"
	mock := &mockRenderConn{remoteFiles: map[string][]byte{"/opt/app/gateway.conf": []byte(content)}}
	ex := cue.NewRenderExecutor(mock)
	shell, ld := singleFileFixture(t, content)
	cr := config.CueRef{Name: "nginx", Nature: "render", Shell: shell, Dest: "gateway.conf", LocalDest: ld}
	r, err := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt/app"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != cue.StatusEqual {
		t.Errorf("content matches remote: want StatusEqual, got %v (err: %v)", r.Status, r.Err)
	}
}

func TestRenderExecutor_single_changed(t *testing.T) {
	mock := &mockRenderConn{remoteFiles: map[string][]byte{"/opt/app/gateway.conf": []byte("old content")}}
	ex := cue.NewRenderExecutor(mock)
	shell, ld := singleFileFixture(t, "new content")
	cr := config.CueRef{Name: "nginx", Nature: "render", Shell: shell, Dest: "gateway.conf", LocalDest: ld}
	r, err := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt/app"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != cue.StatusChanged {
		t.Errorf("content differs: want StatusChanged, got %v (err: %v)", r.Status, r.Err)
	}
}

func TestRenderExecutor_single_dryrun_no_upload(t *testing.T) {
	mock := &mockRenderConn{remoteFiles: map[string][]byte{"/opt/app/gateway.conf": []byte("old")}}
	ex := cue.NewRenderExecutor(mock)
	shell, ld := singleFileFixture(t, "new")
	cr := config.CueRef{Name: "nginx", Nature: "render", Shell: shell, Dest: "gateway.conf", LocalDest: ld}
	ctx := cue.WithCheckOnly(context.Background())
	r, _ := ex.Execute(ctx, nil, cr, config.Target{Dir: "/opt/app"})
	if r.Status != cue.StatusChanged {
		t.Errorf("dry-run: want StatusChanged, got %v (err: %v)", r.Status, r.Err)
	}
	if len(mock.uploadedPaths) > 0 {
		t.Error("dry-run must not upload")
	}
}

func TestRenderExecutor_single_shell_failure(t *testing.T) {
	mock := &mockRenderConn{}
	ex := cue.NewRenderExecutor(mock)
	cr := config.CueRef{Name: "nginx", Nature: "render", Shell: "exit 1", Dest: "gateway.conf"}
	r, _ := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt/app"})
	if r.Status != cue.StatusFailed {
		t.Errorf("shell exit 1: want StatusFailed, got %v", r.Status)
	}
}

func TestRenderExecutor_single_diff_populated(t *testing.T) {
	mock := &mockRenderConn{remoteFiles: map[string][]byte{"/opt/app/gateway.conf": []byte("old content")}}
	ex := cue.NewRenderExecutor(mock)
	shell, ld := singleFileFixture(t, "new content")
	cr := config.CueRef{Name: "nginx", Nature: "render", Shell: shell, Dest: "gateway.conf", LocalDest: ld}
	ctx := cue.WithCheckOnly(context.Background())
	r, _ := ex.Execute(ctx, nil, cr, config.Target{Dir: "/opt/app"})
	if r.Diff == "" {
		t.Errorf("changed single-file render: want non-empty Diff (status=%v err=%v)", r.Status, r.Err)
	}
}

func TestRenderExecutor_single_post_action(t *testing.T) {
	mock := &mockRenderConn{remoteFiles: map[string][]byte{"/opt/app/gateway.conf": []byte("old")}}
	ex := cue.NewRenderExecutor(mock)
	shell, ld := singleFileFixture(t, "new")
	cr := config.CueRef{
		Name:      "nginx",
		Nature:    "render",
		Shell:     shell,
		Dest:      "gateway.conf",
		LocalDest: ld,
		Post:      config.PostAction{Cmd: "nginx -s reload"},
	}
	r, _ := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt/app"})
	if len(r.PostActions) == 0 || r.PostActions[0].Cmd != "nginx -s reload" {
		t.Errorf("want post-action nginx -s reload, got %v (status=%v err=%v)", r.PostActions, r.Status, r.Err)
	}
}

func TestRenderExecutor_nature_and_release_affecting(t *testing.T) {
	mock := &mockRenderConn{remoteFiles: map[string][]byte{"/opt/app/f": []byte("old")}}
	ex := cue.NewRenderExecutor(mock)
	shell, ld := singleFileFixture(t, "new")
	cr := config.CueRef{Name: "r", Nature: "render", Shell: shell, Dest: "f", LocalDest: ld}
	r, _ := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt/app"})
	if r.Nature != "render" {
		t.Errorf("want Nature=render, got %q", r.Nature)
	}
	if r.Status == cue.StatusChanged && !r.IsStateAffecting() {
		t.Error("changed render cue must be release-affecting")
	}
}

// --- folder tests ---

func TestRenderExecutor_folder_changed(t *testing.T) {
	files := map[string]string{"index.html": "<html/>", "main.js": "console.log(1)"}
	mock := &mockRenderConn{
		remoteFiles: map[string][]byte{
			"/opt/app/dist/index.html": []byte("<html/>"),        // same
			"/opt/app/dist/main.js":    []byte("console.log(0)"), // different
		},
	}
	ex := cue.NewRenderExecutor(mock)
	shell, ld := folderFixture(t, files)
	cr := config.CueRef{Name: "frontend", Nature: "render", Shell: shell, Dest: "dist/", LocalDest: ld}
	r, err := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt/app"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != cue.StatusChanged {
		t.Errorf("one file differs: want StatusChanged, got %v (err: %v)", r.Status, r.Err)
	}
}

func TestRenderExecutor_folder_all_equal(t *testing.T) {
	files := map[string]string{"index.html": "<html/>"}
	mock := &mockRenderConn{
		remoteFiles: map[string][]byte{"/opt/app/dist/index.html": []byte("<html/>")},
	}
	ex := cue.NewRenderExecutor(mock)
	shell, ld := folderFixture(t, files)
	cr := config.CueRef{Name: "frontend", Nature: "render", Shell: shell, Dest: "dist/", LocalDest: ld}
	r, _ := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt/app"})
	if r.Status != cue.StatusEqual {
		t.Errorf("all files match: want StatusEqual, got %v (err: %v)", r.Status, r.Err)
	}
}

func TestRenderExecutor_folder_stdout_summary(t *testing.T) {
	files := map[string]string{"a.txt": "new", "b.txt": "same"}
	mock := &mockRenderConn{
		remoteFiles: map[string][]byte{
			"/opt/app/dist/a.txt": []byte("old"),
			"/opt/app/dist/b.txt": []byte("same"),
		},
	}
	ex := cue.NewRenderExecutor(mock)
	shell, ld := folderFixture(t, files)
	cr := config.CueRef{Name: "frontend", Nature: "render", Shell: shell, Dest: "dist/", LocalDest: ld}
	ctx := cue.WithCheckOnly(context.Background())
	r, _ := ex.Execute(ctx, nil, cr, config.Target{Dir: "/opt/app"})
	if r.Stdout == "" {
		t.Errorf("folder render changed: want non-empty Stdout summary (status=%v err=%v)", r.Status, r.Err)
	}
}

func TestRenderExecutor_folder_prune_removes_remote_only(t *testing.T) {
	files := map[string]string{"index.html": "<html/>"}
	mock := &mockRenderConn{
		remoteFiles: map[string][]byte{
			"/opt/app/dist/index.html": []byte("<html/>"),  // same
			"/opt/app/dist/stale.js":   []byte("leftover"), // remote-only — must be pruned
		},
		runOutput: "/opt/app/dist/index.html\n/opt/app/dist/stale.js\n",
	}
	ex := cue.NewRenderExecutor(mock)
	shell, ld := folderFixture(t, files)
	cr := config.CueRef{
		Name:      "frontend",
		Nature:    "render",
		Shell:     shell,
		Dest:      "dist/",
		LocalDest: ld,
		Prune:     boolPtr(true),
	}
	r, err := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt/app"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != cue.StatusChanged {
		t.Errorf("stale file pruned: want StatusChanged, got %v (err: %v)", r.Status, r.Err)
	}
	found := false
	for _, p := range mock.deletedPaths {
		if strings.Contains(p, "stale.js") {
			found = true
		}
	}
	if !found {
		t.Errorf("stale.js must be deleted; deletedPaths=%v", mock.deletedPaths)
	}
}

// --- local_dest tests ---
// When local_dest is set, $ARTIFACT_PATH points there (not a temp file).
// The shell writes rendered output directly to local_dest; regis compares vs remote.

func TestRenderExecutor_localDest_writes_to_path(t *testing.T) {
	// Pre-write content to local_dest; shell is a no-op. Verifies executor reads from LocalDest.
	content := "server { listen 80; }"
	shell, ldPath := singleFileFixture(t, content)

	mock := &mockRenderConn{remoteFiles: map[string][]byte{"/opt/app/gateway.conf": []byte("old")}}
	ex := cue.NewRenderExecutor(mock)
	cr := config.CueRef{
		Name: "nginx", Nature: "render",
		Shell:     shell,
		Dest:      "gateway.conf",
		LocalDest: ldPath,
	}
	_, err := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt/app"})
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := os.ReadFile(ldPath)
	if readErr != nil {
		t.Fatalf("local_dest not readable: %v", readErr)
	}
	if string(got) != content {
		t.Errorf("local_dest content = %q, want %q", string(got), content)
	}
}

func TestRenderExecutor_localDest_equal(t *testing.T) {
	// Rendered content matches remote → StatusEqual.
	content := "same content"
	shell, ldPath := singleFileFixture(t, content)

	mock := &mockRenderConn{remoteFiles: map[string][]byte{"/opt/app/gateway.conf": []byte(content)}}
	ex := cue.NewRenderExecutor(mock)
	cr := config.CueRef{
		Name: "nginx", Nature: "render",
		Shell:     shell,
		Dest:      "gateway.conf",
		LocalDest: ldPath,
	}
	r, _ := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt/app"})
	if r.Status != cue.StatusEqual {
		t.Errorf("rendered == remote: want StatusEqual, got %v (err=%v)", r.Status, r.Err)
	}
}

func TestRenderExecutor_localDest_changed(t *testing.T) {
	// Rendered content differs from remote → StatusChanged + upload.
	shell, ldPath := singleFileFixture(t, "new content")

	mock := &mockRenderConn{remoteFiles: map[string][]byte{"/opt/app/gateway.conf": []byte("old")}}
	ex := cue.NewRenderExecutor(mock)
	cr := config.CueRef{
		Name: "nginx", Nature: "render",
		Shell:     shell,
		Dest:      "gateway.conf",
		LocalDest: ldPath,
	}
	r, _ := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt/app"})
	if r.Status != cue.StatusChanged {
		t.Errorf("rendered ≠ remote: want StatusChanged, got %v (err=%v)", r.Status, r.Err)
	}
	if len(mock.uploadedPaths) == 0 {
		t.Error("want upload to remote, none happened")
	}
}

// TestRenderExecutor_nil_conn_param_no_panic is a non-regression test for the rdiff panic.
// The runner passes nil as the conn parameter in dry-run mode (executors must use e.conn).
// Calling Execute with nil conn must not panic — it must use the stored e.conn.
func TestRenderExecutor_nil_conn_param_no_panic(t *testing.T) {
	mock := &mockRenderConn{remoteFiles: map[string][]byte{"/opt/app/out.conf": []byte("old")}}
	ex := cue.NewRenderExecutor(mock) // conn stored in executor
	shell, ld := singleFileFixture(t, "new")
	cr := config.CueRef{Name: "render-nilconn", Nature: "render", Shell: shell, Dest: "out.conf", LocalDest: ld}
	ctx := cue.WithCheckOnly(context.Background())

	// Pass nil as conn — this is exactly what runner.Run does in dry-run (rdiff) mode.
	// Must not panic; must use e.conn to download and compare.
	r, err := ex.Execute(ctx, nil, cr, config.Target{Dir: "/opt/app"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Status == cue.StatusFailed {
		t.Errorf("nil conn param must not cause failure when e.conn is set; err=%v", r.Err)
	}
}
