// internal/cue/binary_test.go
package cue_test

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
)

// mockConn implements SSHConn for testing executors without a real SSH connection.
type mockConn struct {
	uploadErr  error
	uploadPath string
	uploaded   []byte // last bytes passed to UploadBytes
	// runFunc lets tests configure the response to Run calls.
	runFunc func(cmd string) (string, string, int, error)
	// downloads lets tests provide per-path remote content for Download calls.
	downloads map[string][]byte
}

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
func (m *mockConn) RunSudo(cmd string) (string, string, int, error)  { return "", "", 0, nil }
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
func (m *mockConn) PathSep() string                  { return "/" }

// TestBinaryExecutor_fastPath_mtimeSize: mtime+size match in pre-fetched stats → Equal, no Run calls.
func TestBinaryExecutor_fastPath_mtimeSize(t *testing.T) {
	dir := t.TempDir()
	localPath := dir + "/saver"
	os.WriteFile(localPath, []byte("binary content"), 0755)

	fi, _ := os.Stat(localPath)
	stats := map[string]cue.RemoteStat{
		"/opt/app/saver": {Mtime: fi.ModTime(), Size: fi.Size()},
	}
	ctx := cue.WithRemoteStats(context.Background(), stats)

	var runCalled bool
	mock := &mockConn{runFunc: func(cmd string) (string, string, int, error) {
		runCalled = true
		return "", "", 0, nil
	}}

	ex := cue.NewBinaryExecutor(mock)
	cr := config.CueRef{Name: "bin", Nature: "binary", Src: config.StringOrList{localPath}, Dest: "/opt/app/saver"}
	result, err := ex.Execute(ctx, nil, cr, config.Target{Dir: "/opt/app"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != cue.StatusEqual {
		t.Errorf("want StatusEqual (mtime+size match), got %v", result.Status)
	}
	if runCalled {
		t.Error("expected no SSH calls for fast-path equal")
	}
}

// TestBinaryExecutor_fastPath_hashMatch: mtime/size differ but hash matches → Equal.
func TestBinaryExecutor_fastPath_hashMatch(t *testing.T) {
	dir := t.TempDir()
	localPath := dir + "/saver"
	os.WriteFile(localPath, []byte("binary content"), 0755)

	localHash, _ := cue.LocalHash(localPath)
	stats := map[string]cue.RemoteStat{
		"/opt/app/saver": {Mtime: time.Unix(1, 0), Size: 999, Hash: localHash},
	}
	ctx := cue.WithRemoteStats(context.Background(), stats)

	mock := &mockConn{}
	ex := cue.NewBinaryExecutor(mock)
	cr := config.CueRef{Name: "bin", Nature: "binary", Src: config.StringOrList{localPath}, Dest: "/opt/app/saver"}
	result, err := ex.Execute(ctx, nil, cr, config.Target{Dir: "/opt/app"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != cue.StatusEqual {
		t.Errorf("want StatusEqual (hash match), got %v", result.Status)
	}
}

// TestBinaryExecutor_fallback_unchanged: no pre-fetched stats, stat+hash via Run → Equal.
func TestBinaryExecutor_fallback_unchanged(t *testing.T) {
	dir := t.TempDir()
	localPath := dir + "/saver"
	content := []byte("binary content")
	os.WriteFile(localPath, content, 0755)

	fi, _ := os.Stat(localPath)
	localHash, _ := cue.LocalHash(localPath)

	mock := &mockConn{runFunc: func(cmd string) (string, string, int, error) {
		if strings.Contains(cmd, "stat -c") || strings.Contains(cmd, "stat -f") {
			return fmt.Sprintf("%d %d", fi.ModTime().Unix(), fi.Size()), "", 0, nil
		}
		if strings.Contains(cmd, "md5sum") || strings.Contains(cmd, "md5 -q") {
			return localHash + "  /opt/app/saver", "", 0, nil
		}
		return "", "", 0, nil
	}}

	ex := cue.NewBinaryExecutor(mock)
	cr := config.CueRef{Name: "bin", Nature: "binary", Src: config.StringOrList{localPath}, Dest: "/opt/app/saver"}
	result, err := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt/app"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != cue.StatusEqual {
		t.Errorf("want StatusEqual (mtime+size match), got %v", result.Status)
	}
}

// TestBinaryExecutor_updateMtime_prefetch covers the bulk-prefetch hash-equal branch:
// touch must fire on a real run and on dry-run+WithUpdateMtime, but not on plain dry-run.
func TestBinaryExecutor_updateMtime_prefetch(t *testing.T) {
	dir := t.TempDir()
	localPath := dir + "/saver"
	os.WriteFile(localPath, []byte("binary content"), 0755)
	fi, _ := os.Stat(localPath)
	localHash, _ := cue.LocalHash(localPath)

	// Remote mtime differs from local so the prefetch path has a hash pre-computed.
	stats := map[string]cue.RemoteStat{
		"/opt/app/saver": {Mtime: time.Unix(1, 0), Size: fi.Size(), Hash: localHash},
	}
	cr := config.CueRef{Name: "bin", Nature: "binary", Src: config.StringOrList{localPath}, Dest: "/opt/app/saver"}
	tgt := config.Target{Dir: "/opt/app"}

	cases := []struct {
		name      string
		ctx       context.Context
		wantTouch bool
	}{
		{
			name:      "real run — touch expected",
			ctx:       cue.WithRemoteStats(context.Background(), stats),
			wantTouch: true,
		},
		{
			name:      "dry-run without --update-mtime — no touch",
			ctx:       cue.WithRemoteStats(cue.WithDryRun(context.Background()), stats),
			wantTouch: false,
		},
		{
			name:      "dry-run with --update-mtime — touch expected",
			ctx:       cue.WithRemoteStats(cue.WithUpdateMtime(cue.WithDryRun(context.Background())), stats),
			wantTouch: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var touchCalled bool
			mock := &mockConn{runFunc: func(cmd string) (string, string, int, error) {
				if strings.Contains(cmd, "touch") {
					touchCalled = true
				}
				return "", "", 0, nil
			}}
			ex := cue.NewBinaryExecutor(mock)
			result, err := ex.Execute(tc.ctx, nil, cr, tgt)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != cue.StatusEqual {
				t.Fatalf("want StatusEqual, got %v", result.Status)
			}
			if touchCalled != tc.wantTouch {
				t.Errorf("touchCalled=%v, want %v", touchCalled, tc.wantTouch)
			}
		})
	}
}

// TestBinaryExecutor_updateMtime_fallback covers the individual-SSH hash-equal branch:
// stat returns mismatched mtime/size → triggers hash → hash matches.
// Same touch expectations as the prefetch path.
func TestBinaryExecutor_updateMtime_fallback(t *testing.T) {
	dir := t.TempDir()
	localPath := dir + "/saver"
	os.WriteFile(localPath, []byte("binary content"), 0755)
	localHash, _ := cue.LocalHash(localPath)

	cr := config.CueRef{Name: "bin", Nature: "binary", Src: config.StringOrList{localPath}, Dest: "/opt/app/saver"}
	tgt := config.Target{Dir: "/opt/app"}

	makeConn := func(touchCalled *bool) *mockConn {
		return &mockConn{runFunc: func(cmd string) (string, string, int, error) {
			if strings.Contains(cmd, "stat -c") || strings.Contains(cmd, "stat -f") {
				return "1 1", "", 0, nil // mtime/size differ → triggers hash
			}
			if strings.Contains(cmd, "md5sum") || strings.Contains(cmd, "md5 -q") {
				return localHash + "  /opt/app/saver", "", 0, nil
			}
			if strings.Contains(cmd, "touch") {
				*touchCalled = true
			}
			return "", "", 0, nil
		}}
	}

	cases := []struct {
		name      string
		buildCtx  func() context.Context
		wantTouch bool
	}{
		{
			name:      "real run — touch expected",
			buildCtx:  func() context.Context { return context.Background() },
			wantTouch: true,
		},
		{
			name:      "dry-run without --update-mtime — no touch",
			buildCtx:  func() context.Context { return cue.WithDryRun(context.Background()) },
			wantTouch: false,
		},
		{
			name:      "dry-run with --update-mtime — touch expected",
			buildCtx:  func() context.Context { return cue.WithUpdateMtime(cue.WithDryRun(context.Background())) },
			wantTouch: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var touchCalled bool
			ex := cue.NewBinaryExecutor(makeConn(&touchCalled))
			result, err := ex.Execute(tc.buildCtx(), nil, cr, tgt)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != cue.StatusEqual {
				t.Fatalf("want StatusEqual, got %v", result.Status)
			}
			if touchCalled != tc.wantTouch {
				t.Errorf("touchCalled=%v, want %v", touchCalled, tc.wantTouch)
			}
		})
	}
}

// TestBinaryExecutor_noConn is a non-regression test for the rdiff nil-conn panic.
func TestBinaryExecutor_noConn(t *testing.T) {
	dir := t.TempDir()
	localPath := dir + "/app"
	os.WriteFile(localPath, []byte("binary"), 0755)

	ex := cue.NewBinaryExecutor(nil)
	r, _ := ex.Execute(context.Background(), nil,
		config.CueRef{Name: "app", Nature: "binary", Src: config.StringOrList{localPath}, Dest: "app"},
		config.Target{Dir: "/opt/app"})
	if r.Status != cue.StatusFailed {
		t.Errorf("expected StatusFailed with nil conn, got %v", r.Status)
	}
}

// TestBinaryExecutor_changed: stat differs, hash differs → Changed + post-action + SetMtime called.
func TestBinaryExecutor_changed(t *testing.T) {
	dir := t.TempDir()
	localPath := dir + "/saver"
	os.WriteFile(localPath, []byte("new binary"), 0755)

	var touchCalled bool
	mock := &mockConn{runFunc: func(cmd string) (string, string, int, error) {
		if strings.Contains(cmd, "stat -c") || strings.Contains(cmd, "stat -f") {
			return "1 1", "", 0, nil // mtime/size differ → triggers hash
		}
		if strings.Contains(cmd, "md5sum") || strings.Contains(cmd, "md5 -q") {
			return "differenthash  /opt/app/saver", "", 0, nil
		}
		if strings.Contains(cmd, "touch") {
			touchCalled = true
		}
		return "", "", 0, nil
	}}

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
	if !touchCalled {
		t.Error("expected SetRemoteMtime (touch) call after upload")
	}
}
