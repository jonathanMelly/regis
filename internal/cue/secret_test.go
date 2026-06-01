// internal/cue/secret_test.go
package cue_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
)

type mockConnSecret struct {
	mockConn
	remoteData []byte
}

func (m *mockConnSecret) Download(_ string) ([]byte, error) { return m.remoteData, nil }

func TestSecretExecutor_local_absent_skips(t *testing.T) {
	mock := &mockConnSecret{}
	ex := cue.NewSecretExecutor(mock)
	cr := config.CueRef{
		Name:   "env",
		Nature: "secret",
		Src:    config.StringOrList{"/nonexistent/no-such-file.env"},
		Dest:   ".env",
	}
	result, err := ex.Execute(context.Background(), mock, cr, config.Target{Dir: "/opt/app"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != cue.StatusSkipped {
		t.Errorf("want StatusSkipped when local file absent, got %v", result.Status)
	}
	if mock.uploaded != nil {
		t.Error("must not upload when local file is absent")
	}
}

func TestSecretExecutor_changed(t *testing.T) {
	dir := t.TempDir()
	localPath := dir + "/.env.server"
	os.WriteFile(localPath, []byte("KEY=newvalue\n"), 0600)

	mock := &mockConnSecret{remoteData: []byte("KEY=oldvalue\n")}
	ex := cue.NewSecretExecutor(mock)
	cr := config.CueRef{
		Name:   "env",
		Nature: "secret",
		Src:    config.StringOrList{localPath},
		Dest:   ".env",
	}
	result, err := ex.Execute(context.Background(), mock, cr, config.Target{Dir: "/opt/app"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != cue.StatusChanged {
		t.Errorf("want StatusChanged, got %v", result.Status)
	}
	if mock.uploaded == nil {
		t.Error("expected upload to happen")
	}
}

func TestSecretExecutor_equal(t *testing.T) {
	dir := t.TempDir()
	localPath := dir + "/.env.server"
	os.WriteFile(localPath, []byte("KEY=value\n"), 0600)

	mock := &mockConnSecret{remoteData: []byte("KEY=value\n")}
	ex := cue.NewSecretExecutor(mock)
	cr := config.CueRef{
		Name:   "env",
		Nature: "secret",
		Src:    config.StringOrList{localPath},
		Dest:   ".env",
	}
	result, err := ex.Execute(context.Background(), mock, cr, config.Target{Dir: "/opt/app"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != cue.StatusEqual {
		t.Errorf("want StatusEqual, got %v", result.Status)
	}
	if mock.uploaded != nil {
		t.Error("must not upload when content is equal")
	}
}

func TestSecretExecutor_preserve_keeps_remote_key(t *testing.T) {
	dir := t.TempDir()
	localPath := dir + "/.env.server"
	// local does not include TOKEN
	os.WriteFile(localPath, []byte("KEY=newvalue\n"), 0600)

	// remote has TOKEN — must survive the merge
	mock := &mockConnSecret{remoteData: []byte("KEY=oldvalue\nTOKEN=secret123\n")}
	ex := cue.NewSecretExecutor(mock)
	cr := config.CueRef{
		Name:     "env",
		Nature:   "secret",
		Src:      config.StringOrList{localPath},
		Dest:     ".env",
		Preserve: config.StringOrList{"TOKEN"},
	}
	result, err := ex.Execute(context.Background(), mock, cr, config.Target{Dir: "/opt/app"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != cue.StatusChanged {
		t.Errorf("want StatusChanged (KEY changed), got %v", result.Status)
	}
	if mock.uploaded == nil {
		t.Fatal("expected upload")
	}
	if !strings.Contains(string(mock.uploaded), "TOKEN=secret123") {
		t.Errorf("preserved key TOKEN missing from upload, got:\n%s", mock.uploaded)
	}
}
