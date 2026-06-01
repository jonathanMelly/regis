// internal/cue/binary_test.go
package cue_test

import (
	"context"
	"io/fs"
	"os"
	"testing"
	"time"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
)

// mockConn implements SSHConn for testing executors without a real SSH connection.
type mockConn struct {
	remoteMD5  string
	uploadErr  error
	uploaded   []byte
	uploadPath string
	// runFunc lets tests configure the response to Run calls.
	// Default (nil): returns ("", "", 0, nil).
	runFunc func(cmd string) (string, string, int, error)
	// downloads lets tests provide per-path remote content for Download calls.
	// Default (nil): returns (nil, nil).
	downloads map[string][]byte
}

func (m *mockConn) MD5(path string) (string, error) { return m.remoteMD5, nil }
func (m *mockConn) Upload(local, remote string, mode fs.FileMode, sudo bool) error {
	m.uploadPath = remote
	return m.uploadErr
}
func (m *mockConn) UploadBytes(data []byte, remote string, mode fs.FileMode, sudo bool) error {
	m.uploaded = data
	m.uploadPath = remote
	return m.uploadErr
}
func (m *mockConn) Run(cmd string) (string, string, int, error) {
	if m.runFunc != nil {
		return m.runFunc(cmd)
	}
	return "", "", 0, nil
}
func (m *mockConn) RunSudo(cmd string) (string, string, int, error) { return "", "", 0, nil }
func (m *mockConn) RunWithEnv(cmd string, env map[string]string) (string, string, int, error) {
	return "", "", 0, nil
}
func (m *mockConn) Download(path string) ([]byte, error) {
	if m.downloads != nil {
		if data, ok := m.downloads[path]; ok {
			return data, nil
		}
	}
	return nil, nil
}
func (m *mockConn) Exists(path string) (bool, error) { return true, nil }
func (m *mockConn) Stat(path string) (time.Time, error) { return time.Time{}, nil }
func (m *mockConn) PathSep() string                     { return "/" }

func TestBinaryExecutor_unchanged(t *testing.T) {
	dir := t.TempDir()
	localPath := dir + "/saver"
	content := []byte("binary content")
	os.WriteFile(localPath, content, 0755)

	localMD5, _ := cue.LocalMD5(localPath)
	mock := &mockConn{remoteMD5: localMD5}

	ex := cue.NewBinaryExecutor(mock)
	cr := config.CueRef{
		Name:   "bin",
		Nature: "binary",
		Src:    config.StringOrList{localPath},
		Dest:   "/opt/app/saver",
	}
	result, err := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt/app"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != cue.StatusEqual {
		t.Errorf("want StatusEqual (MD5 match), got %v", result.Status)
	}
}

func TestBinaryExecutor_changed(t *testing.T) {
	dir := t.TempDir()
	localPath := dir + "/saver"
	os.WriteFile(localPath, []byte("new binary"), 0755)

	mock := &mockConn{remoteMD5: "different-md5-hash-here-xxxxxxxxxx"}
	ex := cue.NewBinaryExecutor(mock)
	cr := config.CueRef{
		Name:   "bin",
		Nature: "binary",
		Src:    config.StringOrList{localPath},
		Dest:   "/opt/app/saver",
		Post:   config.PostAction{Cmd: "restart:saver"},
	}
	result, err := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt/app"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != cue.StatusChanged {
		t.Errorf("want StatusChanged, got %v", result.Status)
	}
	if len(result.PostActions) == 0 {
		t.Error("expected post-action collected")
	}
}
