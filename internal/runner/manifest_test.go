// internal/runner/manifest_test.go
package runner_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
	"git.disroot.org/jmy/regis/internal/runner"
)

func TestNewReleaseID_format(t *testing.T) {
	id := runner.NewReleaseID()
	if !strings.HasPrefix(id, "v") {
		t.Errorf("release ID must start with 'v', got %q", id)
	}
	// "v20060102-150405" is 16 chars minimum; longer when on a git tag ("+tag" suffix)
	if len(id) < 16 {
		t.Errorf("release ID too short: want >= 16 chars, got %d (%q)", len(id), id)
	}
}

func TestBuildManifest_checksums_binaryConfigSecret(t *testing.T) {
	results := []cue.Result{
		{CueName: "bin", Nature: "binary", Status: cue.StatusChanged, LocalHash: "aabbcc"},
		{CueName: "cfg", Nature: "config", Status: cue.StatusChanged, LocalHash: "ddeeff"},
		{CueName: "sec", Nature: "secret", Status: cue.StatusChanged, LocalHash: "112233"},
		{CueName: "svc", Nature: "service", Status: cue.StatusChanged, LocalHash: "ignored"},
		{CueName: "act", Nature: "action", Status: cue.StatusChanged, LocalHash: "ignored"},
		{CueName: "rnd", Nature: "render", Status: cue.StatusChanged, LocalHash: "ignored"},
		{CueName: "eq", Nature: "binary", Status: cue.StatusEqual, LocalHash: "skipped"},
	}
	m := runner.BuildManifest("v20260101-120000", []string{"app"}, results, nil, "", false)

	if m.Release != "v20260101-120000" {
		t.Errorf("unexpected release: %s", m.Release)
	}
	if m.Hashes["bin"] != "aabbcc" {
		t.Errorf("binary checksum missing or wrong: %v", m.Hashes)
	}
	if m.Hashes["cfg"] != "ddeeff" {
		t.Errorf("config checksum missing or wrong: %v", m.Hashes)
	}
	if m.Hashes["sec"] != "112233" {
		t.Errorf("secret checksum missing or wrong: %v", m.Hashes)
	}
	// service, action, render — not tracked in checksums
	for _, key := range []string{"svc", "act", "rnd"} {
		if _, ok := m.Hashes[key]; ok {
			t.Errorf("%s must not appear in checksums", key)
		}
	}
	// equal cue — not tracked
	if _, ok := m.Hashes["eq"]; ok {
		t.Error("equal cue must not appear in checksums")
	}
}

func TestBuildManifest_noHashes_whenNoLocalHash(t *testing.T) {
	results := []cue.Result{
		{CueName: "bin", Nature: "binary", Status: cue.StatusChanged}, // no LocalHash
	}
	m := runner.BuildManifest("v1", []string{"s"}, results, nil, "", false)
	if m.Hashes != nil {
		t.Errorf("expected nil checksums when no LocalHash, got %v", m.Hashes)
	}
}

func TestBuildManifest_scenarios(t *testing.T) {
	m := runner.BuildManifest("v1", []string{"app", "db"}, nil, nil, "", false)
	if len(m.Scenarios) != 2 || m.Scenarios[0] != "app" || m.Scenarios[1] != "db" {
		t.Errorf("unexpected scenarios: %v", m.Scenarios)
	}
}

// mockUploader captures the uploaded data for inspection.
type mockUploader struct {
	data       []byte
	remotePath string
}

func (u *mockUploader) UploadBytes(data []byte, remotePath string, _ fs.FileMode, _ bool) error {
	u.data = data
	u.remotePath = remotePath
	return nil
}

func TestWriteManifest_uploadsToCorrectPath(t *testing.T) {
	up := &mockUploader{}
	m := runner.BuildManifest("v20260101-120000", []string{"app"}, nil, nil, "", false)
	if err := runner.WriteManifest(up, "/opt/app", m, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if up.remotePath != "/opt/app/.regis-release" {
		t.Errorf("want /opt/app/.regis-release, got %q", up.remotePath)
	}
	if !strings.Contains(string(up.data), "v20260101-120000") {
		t.Errorf("uploaded YAML does not contain release ID; got:\n%s", up.data)
	}
}

func TestSnapshotRelease_createsManifestAndFiles(t *testing.T) {
	dir := t.TempDir()
	// Write a local source file so SnapshotRelease can read it.
	srcFile := filepath.Join(dir, "app.conf")
	if err := os.WriteFile(srcFile, []byte("key=value"), 0644); err != nil {
		t.Fatal(err)
	}

	steps := []runner.Step{{
		Name:         "cfg",
		ScenarioName: "app",
		CueRef:       config.CueRef{Name: "cfg", Nature: "config", Src: config.StringOrList{srcFile}},
	}}
	manifest := runner.BuildManifest("v20260601-120000", []string{"app"}, []cue.Result{
		{CueName: "cfg", Nature: "config", Status: cue.StatusChanged},
	}, steps, "/opt/app", false)

	releaseDir := filepath.Join(dir, "releases")
	runner.SnapshotRelease(releaseDir, "v20260601-120000", manifest, steps, nil)

	// Manifest file must exist.
	manifestPath := filepath.Join(releaseDir, "v20260601-120000", ".regis-release")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("manifest not written: %v", err)
	}
	if !strings.Contains(string(data), "v20260601-120000") {
		t.Errorf("manifest does not contain release ID; got:\n%s", data)
	}

	// Snapshot of the config file must exist.
	snapFile := filepath.Join(releaseDir, "v20260601-120000", "cfg")
	if _, err := os.Stat(snapFile); err != nil {
		t.Errorf("snapshot file not written: %v", err)
	}
}

func TestSnapshotRelease_emptyIDorDir_isNoop(t *testing.T) {
	dir := t.TempDir()
	manifest := runner.BuildManifest("v1", nil, nil, nil, "", false)
	// Neither call should panic or create files.
	runner.SnapshotRelease("", "v1", manifest, nil, nil)
	runner.SnapshotRelease(dir, "", manifest, nil, nil)
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("expected no files created for empty id/dir, got %d entries", len(entries))
	}
}

func TestPruneLocalSnapshots_keepsNewest(t *testing.T) {
	dir := t.TempDir()
	// Create 7 fake release dirs named v1..v7.
	for _, name := range []string{"v1", "v2", "v3", "v4", "v5", "v6", "v7"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0755); err != nil {
			t.Fatal(err)
		}
	}

	runner.PruneLocalSnapshots(dir, 5)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		t.Errorf("want 5 snapshots after pruning, got %d", len(entries))
	}
}

func TestWriteSnapshot_createsFilesFromBytes(t *testing.T) {
	dir := t.TempDir()
	files := map[string][]byte{
		"app.conf": []byte("key=val"),
		"sub/x.sh": []byte("#!/bin/sh"),
	}
	runner.WriteSnapshot(dir, "v20260601-120000", []byte("release: v20260601-120000\n"), files)

	for name, want := range files {
		p := filepath.Join(dir, "v20260601-120000", filepath.FromSlash(name))
		got, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("file %q not written: %v", name, err)
			continue
		}
		if string(got) != string(want) {
			t.Errorf("file %q: want %q, got %q", name, want, got)
		}
	}
}

func TestWriteSnapshot_emptyIDorDir_isNoop(t *testing.T) {
	dir := t.TempDir()
	runner.WriteSnapshot("", "v1", []byte("x"), nil)
	runner.WriteSnapshot(dir, "", []byte("x"), nil)
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("expected no files for empty id/dir, got %d", len(entries))
	}
}

func TestBuildManifest_cueArtifacts_fromPackResult(t *testing.T) {
	// Pack result provides ArtifactPaths — BuildManifest must populate CueArtifacts.
	results := []cue.Result{
		{
			CueName: "web",
			Nature:  "pack",
			Status:  cue.StatusChanged,
			ArtifactPaths: map[string]string{
				"web/index.html": "/var/www/index.html",
				"web/app.js":     "/var/www/app.js",
			},
		},
	}
	m := runner.BuildManifest("v20260607-120000", []string{"App"}, results, nil, "/opt", false)
	if m.CueArtifacts == nil {
		t.Fatal("CueArtifacts must be populated from pack ArtifactPaths")
	}
	if m.CueArtifacts["web"]["web/index.html"] != "/var/www/index.html" {
		t.Errorf("CueArtifacts[web][web/index.html] wrong: %v", m.CueArtifacts["web"])
	}
	if m.CueArtifacts["web"]["web/app.js"] != "/var/www/app.js" {
		t.Errorf("CueArtifacts[web][web/app.js] wrong: %v", m.CueArtifacts["web"])
	}
}

func TestBuildManifest_cueArtifacts_fromBinaryStep(t *testing.T) {
	steps := []runner.Step{{
		Name: "bin",
		CueRef: config.CueRef{Name: "bin", Nature: "binary", Dest: "bin/app"},
	}}
	m := runner.BuildManifest("v1", []string{"App"}, nil, steps, "/opt/app", false)
	if m.CueArtifacts["bin"]["bin"] != "/opt/app/bin/app" {
		t.Errorf("CueArtifacts[bin][bin] = %q, want /opt/app/bin/app", m.CueArtifacts["bin"]["bin"])
	}
}

func TestSnapshotRelease_packUsesLocalArtifacts(t *testing.T) {
	dir := t.TempDir()
	// Write actual local files that LocalArtifacts will point to.
	localFile1 := filepath.Join(dir, "index.html")
	localFile2 := filepath.Join(dir, "app.js")
	os.WriteFile(localFile1, []byte("<html>current</html>"), 0644)
	os.WriteFile(localFile2, []byte("// current js"), 0644)

	steps := []runner.Step{{
		CueRef: config.CueRef{Name: "web", Nature: "pack"},
	}}
	results := []cue.Result{{
		CueName: "web",
		Nature:  "pack",
		Status:  cue.StatusChanged,
		LocalArtifacts: map[string]string{
			"web/index.html": localFile1,
			"web/app.js":     localFile2,
		},
	}}
	manifest := runner.BuildManifest("v20260607-120000", []string{"App"}, results, steps, "/var/www", false)

	releaseDir := filepath.Join(dir, "releases")
	runner.SnapshotRelease(releaseDir, "v20260607-120000", manifest, steps, results)

	// Pack files must be stored under cueName/relpath keys.
	for _, key := range []string{"web/index.html", "web/app.js"} {
		p := filepath.Join(releaseDir, "v20260607-120000", filepath.FromSlash(key))
		if _, err := os.Stat(p); err != nil {
			t.Errorf("pack snapshot file %q not written: %v", key, err)
		}
	}
}

func TestPruneLocalSnapshots_noopWhenFewEnough(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"v1", "v2", "v3"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0755); err != nil {
			t.Fatal(err)
		}
	}

	runner.PruneLocalSnapshots(dir, 5)

	entries, _ := os.ReadDir(dir)
	if len(entries) != 3 {
		t.Errorf("want 3 snapshots unchanged, got %d", len(entries))
	}
}

// --- force-manifest (includeEqual=true) tests ---
// Covers the manual-sync scenario: files placed on target by hand, regis run sees
// StatusEqual for everything, --force-manifest must still produce a usable manifest.

func TestBuildManifest_forceManifest_binaryUsesLocalHash(t *testing.T) {
	results := []cue.Result{
		{CueName: "bin", Nature: "binary", Status: cue.StatusEqual, LocalHash: "aabbcc"},
	}
	m := runner.BuildManifest("v1", []string{"app"}, results, nil, "", true)
	if m.Hashes["bin"] != "aabbcc" {
		t.Errorf("force-manifest: binary StatusEqual hash not captured; hashes=%v", m.Hashes)
	}
}

func TestBuildManifest_forceManifest_binaryFallsBackToSrcFile(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "app")
	os.WriteFile(srcFile, []byte("binary content"), 0644)

	steps := []runner.Step{{
		CueRef: config.CueRef{Name: "bin", Nature: "binary", Src: config.StringOrList{srcFile}},
	}}
	results := []cue.Result{
		{CueName: "bin", Nature: "binary", Status: cue.StatusEqual}, // no LocalHash
	}
	m := runner.BuildManifest("v1", []string{"app"}, results, steps, "", true)
	if m.Hashes["bin"] == "" {
		t.Error("force-manifest: binary with no LocalHash must fall back to hashing Src[0]")
	}
}

func TestBuildManifest_forceManifest_configSingleSrcHashed(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "app.conf")
	os.WriteFile(srcFile, []byte("key=value"), 0644)

	steps := []runner.Step{{
		CueRef: config.CueRef{Name: "cfg", Nature: "config", Src: config.StringOrList{srcFile}},
	}}
	results := []cue.Result{
		{CueName: "cfg", Nature: "config", Status: cue.StatusEqual},
	}
	m := runner.BuildManifest("v1", []string{"app"}, results, steps, "", true)
	if m.Hashes["cfg"] == "" {
		t.Error("force-manifest: single-src config StatusEqual must be hashed from Src[0]")
	}
}

func TestBuildManifest_forceManifest_configMultiSrcSkipped(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "a.conf")
	f2 := filepath.Join(dir, "b.conf")
	os.WriteFile(f1, []byte("a"), 0644)
	os.WriteFile(f2, []byte("b"), 0644)

	steps := []runner.Step{{
		CueRef: config.CueRef{Name: "cfg", Nature: "config", Src: config.StringOrList{f1, f2}},
	}}
	results := []cue.Result{
		{CueName: "cfg", Nature: "config", Status: cue.StatusEqual},
	}
	m := runner.BuildManifest("v1", []string{"app"}, results, steps, "", true)
	if _, ok := m.Hashes["cfg"]; ok {
		t.Error("force-manifest: multi-src config must not be hashed (no single representative)")
	}
}

func TestBuildManifest_forceManifest_secretSingleSrcHashed(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, ".env")
	os.WriteFile(srcFile, []byte("SECRET=x"), 0644)

	steps := []runner.Step{{
		CueRef: config.CueRef{Name: "sec", Nature: "secret", Src: config.StringOrList{srcFile}},
	}}
	results := []cue.Result{
		{CueName: "sec", Nature: "secret", Status: cue.StatusEqual},
	}
	m := runner.BuildManifest("v1", []string{"app"}, results, steps, "", true)
	if m.Hashes["sec"] == "" {
		t.Error("force-manifest: single-src secret StatusEqual must be hashed from Src[0]")
	}
}

func TestBuildManifest_forceManifest_renderAndActionNotHashed(t *testing.T) {
	results := []cue.Result{
		{CueName: "html", Nature: "render", Status: cue.StatusEqual},
		{CueName: "restart", Nature: "action", Status: cue.StatusEqual},
	}
	m := runner.BuildManifest("v1", []string{"app"}, results, nil, "", true)
	for _, key := range []string{"html", "restart"} {
		if _, ok := m.Hashes[key]; ok {
			t.Errorf("force-manifest: %s must not appear in hashes", key)
		}
	}
}

func TestBuildManifest_forceManifest_changedTakesPrecedenceOverEqual(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "app.conf")
	os.WriteFile(srcFile, []byte("new content"), 0644)

	steps := []runner.Step{{
		CueRef: config.CueRef{Name: "cfg", Nature: "config", Src: config.StringOrList{srcFile}},
	}}
	results := []cue.Result{
		// StatusChanged already has LocalHash set — includeEqual must not overwrite it.
		{CueName: "cfg", Nature: "config", Status: cue.StatusChanged, LocalHash: "original"},
	}
	m := runner.BuildManifest("v1", []string{"app"}, results, steps, "", true)
	if m.Hashes["cfg"] != "original" {
		t.Errorf("force-manifest: StatusChanged hash must not be overwritten by includeEqual pass; got %q", m.Hashes["cfg"])
	}
}

// TestArchiveRelease guards the fix: the archive command must use find(1) to copy
// top-level entries individually, excluding the release dir by name, so it never
// tries to copy a directory into itself when releaseDir is inside targetDir.
func TestArchiveRelease(t *testing.T) {
	var capturedCmd string
	mock := &mockArchiveConn{runFn: func(cmd string) (string, string, int, error) {
		capturedCmd = cmd
		return "", "", 0, nil
	}}

	err := runner.ArchiveRelease(mock, "/srv/app", "/srv/app/.regis-releases", "v20260101-120000")
	if err != nil {
		t.Fatalf("ArchiveRelease returned error: %v", err)
	}

	// Must use find, not plain "cp -rp targetDir/."
	if strings.Contains(capturedCmd, "cp -rp /srv/app/.") {
		t.Error("archive command must not use 'cp -rp targetDir/.' — it copies the release dir into itself")
	}
	if !strings.Contains(capturedCmd, "find") {
		t.Error("archive command must use find to enumerate top-level entries")
	}
	// Must exclude the release dir base name (.regis-releases).
	if !strings.Contains(capturedCmd, ".regis-releases") || !strings.Contains(capturedCmd, "! -name") {
		t.Error("archive command must exclude release dir base name via ! -name")
	}
	// Destination must include the release ID.
	if !strings.Contains(capturedCmd, "v20260101-120000") {
		t.Error("archive command must include release ID in destination path")
	}
}

type mockArchiveConn struct {
	runFn func(cmd string) (string, string, int, error)
}

func (m *mockArchiveConn) Run(cmd string) (string, string, int, error) { return m.runFn(cmd) }
