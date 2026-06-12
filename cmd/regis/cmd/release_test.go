// cmd/regis/cmd/release_test.go
package cmd

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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

// ── hashesEqual ────────────────────────────────────────────────────────────

func TestHashesEqual_identical(t *testing.T) {
	a := map[string]string{"bin": "aabb", "cfg": "ccdd"}
	b := map[string]string{"bin": "aabb", "cfg": "ccdd"}
	if !hashesEqual(a, b) {
		t.Error("expected equal")
	}
}

func TestHashesEqual_valueDiffer(t *testing.T) {
	a := map[string]string{"bin": "aabb"}
	b := map[string]string{"bin": "xxxx"}
	if hashesEqual(a, b) {
		t.Error("expected not equal")
	}
}

func TestHashesEqual_lengthDiffer(t *testing.T) {
	a := map[string]string{"bin": "aabb", "cfg": "ccdd"}
	b := map[string]string{"bin": "aabb"}
	if hashesEqual(a, b) {
		t.Error("expected not equal when lengths differ")
	}
}

func TestHashesEqual_bothNil(t *testing.T) {
	if !hashesEqual(nil, nil) {
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
		Hashes:  checksums,
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
		Hashes:  map[string]string{"bin": "aabbccdd"},
	}
	remoteM := runner.ReleaseManifest{
		Release:    "v20260603-120000",
		DeployedAt: time.Now().Add(-time.Hour),
		Hashes:  map[string]string{"bin": "xxxxxxxx"}, // different checksum
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

// ── buildCheckEntries ─────────────────────────────────────────────────────────

func makeCfg(cues ...config.CueRef) *config.Config {
	sc := config.Scenario{Cues: cues}
	return &config.Config{
		ScenarioNames: []string{"app"},
		Scenarios:     map[string]config.Scenario{"app": sc},
	}
}

func TestBuildCheckEntries_binary(t *testing.T) {
	cfg := makeCfg(config.CueRef{Name: "bin", Nature: "binary", Dest: "/opt/app/bin"})
	m := runner.ReleaseManifest{Hashes: map[string]string{"bin": "aabbcc"}}
	entries := buildCheckEntries(cfg, m, "/opt/app")
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if !e.trackHash {
		t.Error("binary entry must have trackHash=true")
	}
	if e.expectedHash != "aabbcc" {
		t.Errorf("expectedHash: want aabbcc, got %q", e.expectedHash)
	}
}

func TestBuildCheckEntries_pack_withArtifacts(t *testing.T) {
	cfg := makeCfg(config.CueRef{Name: "web", Nature: "pack", Dest: "/www"})
	m := runner.ReleaseManifest{
		CueArtifacts: map[string]map[string]string{
			"web": {
				"web/index.html": "/www/index.html",
				"web/app.js":     "/www/app.js",
			},
		},
		FileHashes: map[string]map[string]string{
			"web": {
				"web/index.html": "hash1",
				"web/app.js":     "hash2",
			},
		},
	}
	entries := buildCheckEntries(cfg, m, "/opt")
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.baseCueName != "web" {
			t.Errorf("baseCueName: want web, got %q", e.baseCueName)
		}
		if !e.trackHash {
			t.Errorf("entry %q: want trackHash=true (FileHashes present)", e.cueName)
		}
		if e.expectedHash == "" {
			t.Errorf("entry %q: expectedHash must not be empty when FileHashes present", e.cueName)
		}
	}
}

func TestBuildCheckEntries_pack_noArtifacts_sentinel(t *testing.T) {
	cfg := makeCfg(config.CueRef{Name: "web", Nature: "pack", Dest: "/www"})
	m := runner.ReleaseManifest{} // no artifacts recorded
	entries := buildCheckEntries(cfg, m, "/opt")
	if len(entries) != 1 {
		t.Fatalf("want 1 sentinel entry, got %d", len(entries))
	}
	if entries[0].remotePath != "" {
		t.Error("sentinel entry must have remotePath=\"\"")
	}
}

func TestBuildCheckEntries_pack_olderManifest_noFileHashes(t *testing.T) {
	// Older manifest: has CueArtifacts but no FileHashes — pack files show as presence-only.
	cfg := makeCfg(config.CueRef{Name: "web", Nature: "pack", Dest: "/www"})
	m := runner.ReleaseManifest{
		CueArtifacts: map[string]map[string]string{
			"web": {"web/index.html": "/www/index.html"},
		},
		// FileHashes intentionally absent
	}
	entries := buildCheckEntries(cfg, m, "/opt")
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	if entries[0].trackHash {
		t.Error("older manifest without FileHashes: trackHash must be false")
	}
}

func TestBuildCheckEntries_render_singleFile(t *testing.T) {
	cfg := makeCfg(config.CueRef{Name: "tmpl", Nature: "render", Dest: "/etc/app/conf"})
	m := runner.ReleaseManifest{}
	entries := buildCheckEntries(cfg, m, "/opt")
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	if entries[0].trackHash {
		t.Error("render file must have trackHash=false")
	}
}

func TestBuildCheckEntries_manifestFallback(t *testing.T) {
	// Config has no cues, but manifest has a removed cue — should appear via fallback.
	cfg := makeCfg() // empty
	m := runner.ReleaseManifest{
		Artifacts: map[string]string{"old-bin": "/opt/old-bin"},
		Hashes:    map[string]string{"old-bin": "deadbeef"},
	}
	entries := buildCheckEntries(cfg, m, "/opt")
	if len(entries) != 1 {
		t.Fatalf("want 1 fallback entry, got %d", len(entries))
	}
	e := entries[0]
	if !e.trackHash || e.expectedHash != "deadbeef" {
		t.Errorf("fallback entry: want trackHash=true expectedHash=deadbeef, got trackHash=%v expectedHash=%q",
			e.trackHash, e.expectedHash)
	}
}

func TestBuildCheckEntries_noDuplication(t *testing.T) {
	// Same cue in both config and manifest.Artifacts must not produce duplicate entries.
	cfg := makeCfg(config.CueRef{Name: "bin", Nature: "binary", Dest: "/opt/bin"})
	m := runner.ReleaseManifest{
		Artifacts: map[string]string{"bin": "/opt/bin"},
		Hashes:    map[string]string{"bin": "aabbcc"},
	}
	entries := buildCheckEntries(cfg, m, "/opt")
	if len(entries) != 1 {
		t.Errorf("want exactly 1 entry (no duplication), got %d", len(entries))
	}
}

// ── latestLocalSnapshot ───────────────────────────────────────────────────────

func TestLatestLocalSnapshot_returnsNewest(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"v20260610-120000", "v20260612-090000", "v20260611-150000"} {
		snap := filepath.Join(dir, id)
		os.MkdirAll(snap, 0755)
		os.WriteFile(filepath.Join(snap, ".regis-release"), []byte("release: "+id), 0644)
	}
	got := latestLocalSnapshot(dir)
	if !strings.Contains(got, "v20260612-090000") {
		t.Errorf("want latest snapshot (v20260612-090000), got %q", got)
	}
}

func TestLatestLocalSnapshot_empty(t *testing.T) {
	if got := latestLocalSnapshot(t.TempDir()); got != "" {
		t.Errorf("want empty string for empty dir, got %q", got)
	}
}
