// internal/cue/config_test.go
package cue_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
)

// mockConnWithDownload embeds mockConn and overrides Download to return preset content.
type mockConnWithDownload struct {
	mockConn
	remoteContent string
}

func (m *mockConnWithDownload) Download(path string) ([]byte, error) {
	return []byte(m.remoteContent), nil
}

// TestConfigExecutor_noConn is a non-regression test for the rdiff nil-conn panic.
// When the SSH dial fails, rdiff passes nil as conn; executor must return StatusFailed, not panic.
func TestConfigExecutor_noConn(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/nginx.conf", []byte("server {}"), 0644)

	ex := cue.NewConfigExecutor(nil)
	r, _ := ex.Execute(context.Background(), nil,
		config.CueRef{Name: "nginx", Nature: "config", Src: config.StringOrList{dir + "/nginx.conf"}, Dest: "/etc/nginx/nginx.conf"},
		config.Target{Dir: "/opt"})
	if r.Status != cue.StatusFailed {
		t.Errorf("expected StatusFailed with nil conn, got %v", r.Status)
	}
}

func TestConfigExecutor_changed(t *testing.T) {
	dir := t.TempDir()
	localPath := dir + "/gateway.conf"
	os.WriteFile(localPath, []byte("server { listen 80; }\n"), 0644)

	mock := &mockConnWithDownload{remoteContent: "server { listen 8080; }\n"}
	ex := cue.NewConfigExecutor(mock)
	cr := config.CueRef{
		Name:   "gateway-conf",
		Nature: "config",
		Src:    config.StringOrList{localPath},
		Dest:   "/etc/nginx/conf.d/gateway.conf",
		Post:   config.PostAction{Cmd: "nginx -t && nginx -s reload", Sudo: true},
	}
	result, err := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt/app"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != cue.StatusChanged {
		t.Errorf("want StatusChanged, got %v", result.Status)
	}
	if result.Diff == "" {
		t.Error("expected diff text")
	}
	if len(result.PostActions) == 0 {
		t.Error("expected post-action")
	}
}

func TestConfigExecutor_unchanged(t *testing.T) {
	dir := t.TempDir()
	localPath := dir + "/gateway.conf"
	content := "server { listen 80; }\n"
	os.WriteFile(localPath, []byte(content), 0644)

	mock := &mockConnWithDownload{remoteContent: content}
	ex := cue.NewConfigExecutor(mock)
	cr := config.CueRef{
		Name: "gateway-conf", Nature: "config",
		Src: config.StringOrList{localPath}, Dest: "/etc/nginx/conf.d/gateway.conf",
	}
	result, _ := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt/app"})
	if result.Status != cue.StatusEqual {
		t.Errorf("want StatusEqual, got %v", result.Status)
	}
}

func TestConfigExecutor_multiSrc_changed(t *testing.T) {
	dir := t.TempDir()
	f1 := dir + "/a.conf"
	f2 := dir + "/b.conf"
	os.WriteFile(f1, []byte("a new\n"), 0644)
	os.WriteFile(f2, []byte("b new\n"), 0644)

	// remote has different content for both files
	mock := &mockConn{downloads: map[string][]byte{
		"/etc/nginx/conf.d/a.conf": []byte("a old\n"),
		"/etc/nginx/conf.d/b.conf": []byte("b old\n"),
	}}
	ex := cue.NewConfigExecutor(mock)
	cr := config.CueRef{
		Name:   "nginx-snippets",
		Nature: "config",
		Src:    config.StringOrList{f1, f2},
		Dest:   "/etc/nginx/conf.d/",
	}
	result, err := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt/app"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != cue.StatusChanged {
		t.Errorf("want StatusChanged, got %v", result.Status)
	}
	if result.Diff == "" {
		t.Error("expected aggregated diff")
	}
	if !strings.Contains(result.Diff, "a.conf") || !strings.Contains(result.Diff, "b.conf") {
		t.Errorf("diff should mention both files, got:\n%s", result.Diff)
	}
}

func TestConfigExecutor_multiSrc_unchanged(t *testing.T) {
	dir := t.TempDir()
	f1 := dir + "/a.conf"
	f2 := dir + "/b.conf"
	os.WriteFile(f1, []byte("same\n"), 0644)
	os.WriteFile(f2, []byte("same\n"), 0644)

	mock := &mockConn{downloads: map[string][]byte{
		"/etc/nginx/conf.d/a.conf": []byte("same\n"),
		"/etc/nginx/conf.d/b.conf": []byte("same\n"),
	}}
	ex := cue.NewConfigExecutor(mock)
	cr := config.CueRef{
		Name:   "nginx-snippets",
		Nature: "config",
		Src:    config.StringOrList{f1, f2},
		Dest:   "/etc/nginx/conf.d/",
	}
	result, _ := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt/app"})
	if result.Status != cue.StatusEqual {
		t.Errorf("want StatusEqual, got %v", result.Status)
	}
}

func TestConfigExecutor_multiSrc_partialChange(t *testing.T) {
	dir := t.TempDir()
	f1 := dir + "/a.conf"
	f2 := dir + "/b.conf"
	os.WriteFile(f1, []byte("a new\n"), 0644)
	os.WriteFile(f2, []byte("b same\n"), 0644)

	mock := &mockConn{downloads: map[string][]byte{
		"/etc/nginx/conf.d/a.conf": []byte("a old\n"),
		"/etc/nginx/conf.d/b.conf": []byte("b same\n"),
	}}
	ex := cue.NewConfigExecutor(mock)
	cr := config.CueRef{
		Name:   "nginx-snippets",
		Nature: "config",
		Src:    config.StringOrList{f1, f2},
		Dest:   "/etc/nginx/conf.d/",
	}
	result, _ := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt/app"})
	if result.Status != cue.StatusChanged {
		t.Errorf("want StatusChanged when any file changes, got %v", result.Status)
	}
	if !strings.Contains(result.Diff, "a.conf") {
		t.Error("diff should mention changed file a.conf")
	}
	if strings.Contains(result.Diff, "b.conf") {
		t.Error("diff should not mention unchanged file b.conf")
	}
}

func TestConfigExecutor_globSrc(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/a.conf", []byte("a\n"), 0644)
	os.WriteFile(dir+"/b.conf", []byte("b\n"), 0644)

	mock := &mockConn{} // no remote content → first deploy, all changed
	ex := cue.NewConfigExecutor(mock)
	cr := config.CueRef{
		Name:   "snippets",
		Nature: "config",
		Src:    config.StringOrList{dir + "/*.conf"},
		Dest:   "/etc/nginx/conf.d/",
	}
	result, err := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt/app"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != cue.StatusChanged {
		t.Errorf("want StatusChanged for glob src, got %v", result.Status)
	}
}

func TestConfigExecutor_multiSrc_dryRun(t *testing.T) {
	dir := t.TempDir()
	f1 := dir + "/a.conf"
	os.WriteFile(f1, []byte("new\n"), 0644)

	uploaded := false
	mock := &mockConn{
		downloads: map[string][]byte{"/etc/nginx/conf.d/a.conf": []byte("old\n")},
		uploadErr: nil,
	}
	// Intercept UploadBytes to detect accidental upload in dry-run.
	origUpload := mock.uploadErr // placeholder; we check uploadPath stays empty
	_ = origUpload

	ex := cue.NewConfigExecutor(mock)
	cr := config.CueRef{
		Name:   "snippets",
		Nature: "config",
		Src:    config.StringOrList{f1},
		Dest:   "/etc/nginx/conf.d/",
		Post:   config.PostAction{Cmd: "nginx -s reload"},
	}
	ctx := cue.WithCheckOnly(context.Background())
	result, err := ex.Execute(ctx, nil, cr, config.Target{Dir: "/opt/app"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != cue.StatusChanged {
		t.Errorf("want StatusChanged in dry-run, got %v", result.Status)
	}
	if result.Diff == "" {
		t.Error("expected diff even in dry-run")
	}
	if len(result.PostActions) != 0 {
		t.Error("dry-run must not collect post-actions")
	}
	// Verify no upload happened.
	if mock.uploadPath != "" || uploaded {
		t.Error("dry-run must not upload")
	}
}

func TestSecretExecutor_masksValuesInDiff(t *testing.T) {
	dir := t.TempDir()
	localPath := dir + "/.env"
	os.WriteFile(localPath, []byte("TOKEN=newsecret\nOTHER=val\n"), 0600)

	mock := &mockConnWithDownload{remoteContent: "TOKEN=oldsecret\nOTHER=val\n"}
	ex := cue.NewSecretExecutor(mock)
	cr := config.CueRef{
		Name:     "env",
		Nature:   "secret",
		Src:      config.StringOrList{localPath},
		Dest:     ".env",
		Preserve: config.StringOrList{},
		Mode:     "600",
	}
	result, _ := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt/app"})
	if result.Status != cue.StatusChanged {
		t.Errorf("want StatusChanged, got %v", result.Status)
	}
	if result.Diff == "" {
		t.Error("expected diff")
	}
	// Values must NOT appear in diff
	for _, secret := range []string{"newsecret", "oldsecret"} {
		if strings.Contains(result.Diff, secret) {
			t.Errorf("diff must not contain secret value %q:\n%s", secret, result.Diff)
		}
	}
}
