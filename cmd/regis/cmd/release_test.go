// cmd/regis/cmd/release_test.go
package cmd

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/runner"
)

// mockConn implements releaseConn for tests.
// runCode controls the exit code returned by Run (used for "test -d" checks).
// dlData / dlErr control what Download returns.
type mockConn struct {
	runCode int
	dlData  []byte
	dlErr   error
}

func (m *mockConn) Run(_ string) (string, string, int, error) { return "", "", m.runCode, nil }
func (m *mockConn) Download(_ string) ([]byte, error)        { return m.dlData, m.dlErr }
func (m *mockConn) Upload(_, _ string, _ fs.FileMode, _ bool) error { return nil }
func (m *mockConn) UploadBytes(_ []byte, _ string, _ fs.FileMode, _ bool) error { return nil }

// marshalManifest is a test helper that serialises a ReleaseManifest to YAML bytes.
func marshalManifest(t *testing.T, m runner.ReleaseManifest) []byte {
	t.Helper()
	b, err := yaml.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	return b
}

// writeLocalManifest writes a .regis-release file into a temp snapshot dir and returns the dir path.
func writeLocalManifest(t *testing.T, releaseID string, m runner.ReleaseManifest) string {
	t.Helper()
	dir := t.TempDir()
	snapDir := filepath.Join(dir, releaseID)
	if err := os.MkdirAll(snapDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapDir, ".regis-release"), marshalManifest(t, m), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// ── checksumsEqual ────────────────────────────────────────────────────────────

func TestChecksumsEqual_identical(t *testing.T) {
	a := map[string]string{"bin": "aabb", "cfg": "ccdd"}
	b := map[string]string{"bin": "aabb", "cfg": "ccdd"}
	if !checksumsEqual(a, b) {
		t.Error("expected equal")
	}
}

func TestChecksumsEqual_valueDiffer(t *testing.T) {
	a := map[string]string{"bin": "aabb"}
	b := map[string]string{"bin": "xxxx"}
	if checksumsEqual(a, b) {
		t.Error("expected not equal")
	}
}

func TestChecksumsEqual_lengthDiffer(t *testing.T) {
	a := map[string]string{"bin": "aabb", "cfg": "ccdd"}
	b := map[string]string{"bin": "aabb"}
	if checksumsEqual(a, b) {
		t.Error("expected not equal when lengths differ")
	}
}

func TestChecksumsEqual_bothNil(t *testing.T) {
	if !checksumsEqual(nil, nil) {
		t.Error("two nil maps should be equal")
	}
}

// ── effectiveLocalDir ─────────────────────────────────────────────────────────

func TestEffectiveLocalDir_default(t *testing.T) {
	cfg := &config.Config{}
	if got := effectiveLocalDir(cfg); got != ".regis-releases" {
		t.Errorf("want .regis-releases, got %q", got)
	}
}

func TestEffectiveLocalDir_custom(t *testing.T) {
	cfg := &config.Config{Release: config.ReleaseConfig{LocalDir: "/tmp/my-releases"}}
	if got := effectiveLocalDir(cfg); got != "/tmp/my-releases" {
		t.Errorf("want /tmp/my-releases, got %q", got)
	}
}

// ── shortHash ─────────────────────────────────────────────────────────────────

func TestShortHash_long(t *testing.T) {
	if got := shortHash("aabbccdd11223344"); got != "aabbccdd" {
		t.Errorf("want first 8 chars, got %q", got)
	}
}

func TestShortHash_short(t *testing.T) {
	if got := shortHash("abc"); got != "abc" {
		t.Errorf("short string should pass through, got %q", got)
	}
}

// ── releasePreflight ──────────────────────────────────────────────────────────

func TestReleasePreflight_neither(t *testing.T) {
	conn := &mockConn{runCode: 1} // test -d → not found
	localDir := t.TempDir()       // empty, no release subdir

	pf := releasePreflight(conn, "v20260603-120000", "/opt/releases", localDir)
	if pf.action != preflightStop {
		t.Errorf("want preflightStop, got %v", pf.action)
	}
}

func TestReleasePreflight_remoteOnly(t *testing.T) {
	conn := &mockConn{runCode: 0} // test -d → exists
	localDir := t.TempDir()       // empty, no local snapshot

	pf := releasePreflight(conn, "v20260603-120000", "/opt/releases", localDir)
	if pf.action != preflightOK {
		t.Errorf("want preflightOK, got %v (summary: %s)", pf.action, pf.summary)
	}
	if !pf.remoteExists || pf.localExists {
		t.Error("expected remoteExists=true, localExists=false")
	}
}

func TestReleasePreflight_localOnly(t *testing.T) {
	conn := &mockConn{runCode: 1} // test -d → not found remotely
	m := runner.ReleaseManifest{Release: "v20260603-120000", DeployedAt: time.Now()}
	localDir := writeLocalManifest(t, "v20260603-120000", m)

	pf := releasePreflight(conn, "v20260603-120000", "/opt/releases", localDir)
	if pf.action != preflightWarn {
		t.Errorf("want preflightWarn, got %v (summary: %s)", pf.action, pf.summary)
	}
	if !pf.localExists || pf.remoteExists {
		t.Errorf("expected localExists=true, remoteExists=false; got local=%v remote=%v", pf.localExists, pf.remoteExists)
	}
}

func TestReleasePreflight_bothMatch(t *testing.T) {
	checksums := map[string]string{"bin": "aabbccdd", "cfg": "11223344"}
	m := runner.ReleaseManifest{
		Release:    "v20260603-120000",
		DeployedAt: time.Now(),
		Checksums:  checksums,
	}
	localDir := writeLocalManifest(t, "v20260603-120000", m)
	conn := &mockConn{
		runCode: 0, // test -d → exists
		dlData:  marshalManifest(t, m),
	}

	pf := releasePreflight(conn, "v20260603-120000", "/opt/releases", localDir)
	if pf.action != preflightOK {
		t.Errorf("want preflightOK, got %v (summary: %s reason: %s)", pf.action, pf.summary, pf.reason)
	}
	if pf.diverged {
		t.Error("expected not diverged")
	}
}

func TestReleasePreflight_bothMismatch(t *testing.T) {
	localM := runner.ReleaseManifest{
		Release:    "v20260603-120000",
		DeployedAt: time.Now(),
		Checksums:  map[string]string{"bin": "aabbccdd"},
	}
	remoteM := runner.ReleaseManifest{
		Release:    "v20260603-120000",
		DeployedAt: time.Now().Add(-time.Hour),
		Checksums:  map[string]string{"bin": "xxxxxxxx"}, // different checksum
	}
	localDir := writeLocalManifest(t, "v20260603-120000", localM)
	conn := &mockConn{
		runCode: 0,
		dlData:  marshalManifest(t, remoteM),
	}

	pf := releasePreflight(conn, "v20260603-120000", "/opt/releases", localDir)
	if pf.action != preflightStop {
		t.Errorf("want preflightStop on mismatch, got %v", pf.action)
	}
	if !pf.diverged {
		t.Error("expected diverged=true")
	}
}

// ── reuploadFromLocal ─────────────────────────────────────────────────────────

func TestReuploadFromLocal_uploadsArtifacts(t *testing.T) {
	releaseID := "v20260603-120000"
	snapshotDir := t.TempDir()
	snapRelDir := filepath.Join(snapshotDir, releaseID)
	if err := os.MkdirAll(snapRelDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write a fake binary artifact file.
	if err := os.WriteFile(filepath.Join(snapRelDir, "myapp"), []byte("binarydata"), 0755); err != nil {
		t.Fatal(err)
	}

	m := runner.ReleaseManifest{
		Release:   releaseID,
		Artifacts: map[string]string{"myapp": "/opt/app/myapp"},
	}
	if err := os.WriteFile(filepath.Join(snapRelDir, ".regis-release"), marshalManifest(t, m), 0644); err != nil {
		t.Fatal(err)
	}

	var uploadedLocal, uploadedRemote string
	conn := &uploadTrackConn{}
	tgt := config.Target{Dir: "/opt/app", Sudo: false}

	if err := reuploadFromLocal(conn, releaseID, snapshotDir, tgt); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = uploadedLocal
	_ = uploadedRemote
	if conn.uploadLocalPath != filepath.Join(snapRelDir, "myapp") {
		t.Errorf("unexpected upload local path: %q", conn.uploadLocalPath)
	}
	if conn.uploadRemotePath != "/opt/app/myapp" {
		t.Errorf("unexpected upload remote path: %q", conn.uploadRemotePath)
	}
}

// uploadTrackConn records the last Upload call.
type uploadTrackConn struct {
	uploadLocalPath  string
	uploadRemotePath string
}

func (u *uploadTrackConn) Run(_ string) (string, string, int, error)         { return "", "", 0, nil }
func (u *uploadTrackConn) Download(_ string) ([]byte, error)                 { return nil, nil }
func (u *uploadTrackConn) UploadBytes(_ []byte, _ string, _ fs.FileMode, _ bool) error { return nil }
func (u *uploadTrackConn) Upload(local, remote string, _ fs.FileMode, _ bool) error {
	u.uploadLocalPath = local
	u.uploadRemotePath = remote
	return nil
}
