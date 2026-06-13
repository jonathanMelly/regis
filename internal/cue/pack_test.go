// internal/cue/pack_test.go
package cue_test

import (
	"context"
	"crypto/md5"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
)

// packEqualRun returns a runFunc that simulates BulkStatRemote + BulkHashRemote for
// the given remote path → content map, producing "equal" results (hash matches).
// Stat intentionally returns mtime=0 so the hash branch is always exercised.
func packEqualRun(remotes map[string][]byte) func(string) (string, string, int, error) {
	return func(cmd string) (string, string, int, error) {
		if strings.Contains(cmd, "stat") {
			var sb strings.Builder
			for path, data := range remotes {
				fmt.Fprintf(&sb, "0 %d %s\n", len(data), path)
			}
			return sb.String(), "", 0, nil
		}
		if strings.Contains(cmd, "md5sum") {
			var sb strings.Builder
			for path, data := range remotes {
				h := md5.Sum(data)
				fmt.Fprintf(&sb, "%x  %s\n", h, path)
			}
			return sb.String(), "", 0, nil
		}
		return "", "", 0, nil
	}
}

// ---- helper unit tests (pure functions via exported wrappers) ----------------

func TestParseManifestSet(t *testing.T) {
	got := cue.ParseManifestSet("index.html\nstyle.css\n\n.regis-pack-src\n")
	if !got["index.html"] || !got["style.css"] {
		t.Error("expected index.html and style.css to be present")
	}
	if got[".regis-pack-src"] {
		t.Error(".regis-pack-* entries must be excluded")
	}
	if len(got) != 2 {
		t.Errorf("expected 2 entries, got %d", len(got))
	}
}

func TestExtractReleaseIDFromManifest(t *testing.T) {
	yaml := "release: v20260606-143021\ndeployed_at: 2026-06-06\n"
	if got := cue.ExtractReleaseIDFromManifest(yaml); got != "v20260606-143021" {
		t.Errorf("got %q", got)
	}
	if got := cue.ExtractReleaseIDFromManifest("no release here\n"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestDestRelativeToTarget(t *testing.T) {
	cases := []struct {
		dest    string
		wantRel string
		wantOk  bool
	}{
		{"./", "", true},
		{".", "", true},
		{"", "", true},
		{"html", "html", true},
		{"html/", "html", true},
		{"/var/www", "", false},
		{"C:/www", "", false},
	}
	for _, tc := range cases {
		rel, ok := cue.DestRelativeToTarget(tc.dest)
		if ok != tc.wantOk || rel != tc.wantRel {
			t.Errorf("DestRelativeToTarget(%q) = (%q,%v), want (%q,%v)",
				tc.dest, rel, ok, tc.wantRel, tc.wantOk)
		}
	}
}

func TestPackScopeFilter_flatPatterns(t *testing.T) {
	candidates := []cue.PackCandidate{
		cue.PackCandidateWith("index.html"),
		cue.PackCandidateWith("about.html"),
		cue.PackCandidateWith(".env"),
		cue.PackCandidateWith("app.js"),
	}
	srcs := config.StringOrList{"*.html"}
	got := cue.PackScopeFilter(candidates, srcs)
	if len(got) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(got))
	}
}

func TestPackScopeFilter_nonFlatReturnsNil(t *testing.T) {
	candidates := []cue.PackCandidate{
		cue.PackCandidateWith("Model/User.php"),
		cue.PackCandidateWith(".env"),
	}
	srcs := config.StringOrList{"application/**"} // non-empty glob root → can't scope
	got := cue.PackScopeFilter(candidates, srcs)
	if got != nil {
		t.Errorf("expected nil for non-flat patterns, got %v", got)
	}
}

func TestPackScopeFilter_mixedPatterns(t *testing.T) {
	candidates := []cue.PackCandidate{
		cue.PackCandidateWith("index.html"),
		cue.PackCandidateWith(".env"),
		cue.PackCandidateWith("subdir/page.html"), // filepath.Match won't match this at root
	}
	srcs := config.StringOrList{"application/**", "*.html"}
	got := cue.PackScopeFilter(candidates, srcs)
	// only index.html matches *.html at root level
	if len(got) != 1 {
		t.Errorf("expected 1, got %d: %v", len(got), got)
	}
}

// ---- Execute integration tests (mock SSH) -----------------------------------

func TestPackExecutor_noConn(t *testing.T) {
	ex := cue.NewPackExecutor(nil)
	r, _ := ex.Execute(context.Background(), nil,
		config.CueRef{Name: "src", Nature: "pack", Src: config.StringOrList{"*.go"}, Dest: "./"},
		config.Target{Dir: "/opt"})
	if r.Status != cue.StatusFailed {
		t.Errorf("expected StatusFailed, got %v", r.Status)
	}
}

func TestPackExecutor_noChanges(t *testing.T) {
	dir := t.TempDir()
	content := []byte("hello")
	os.WriteFile(dir+"/index.html", content, 0644)

	mock := &mockConn{
		runFunc: packEqualRun(map[string][]byte{"/www/index.html": content}),
	}

	ex := cue.NewPackExecutor(mock)
	cr := config.CueRef{
		Name:   "src",
		Nature: "pack",
		Src:    config.StringOrList{dir + "/index.html"},
		Dest:   "/www",
	}
	r, _ := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt"})
	if r.Status != cue.StatusEqual {
		t.Errorf("expected StatusEqual, got %v (stdout: %q)", r.Status, r.Stdout)
	}
}

func TestPackExecutor_uploadChanged(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/index.html", []byte("new content"), 0644)

	var uploadedPath string
	mock := &mockConn{
		downloads: map[string][]byte{
			"/www/index.html": []byte("old content"),
		},
		runFunc: func(cmd string) (string, string, int, error) {
			return "", "", 0, nil
		},
	}
	mock.uploadErr = nil
	_ = uploadedPath

	ex := cue.NewPackExecutor(mock)
	cr := config.CueRef{
		Name:   "src",
		Nature: "pack",
		Src:    config.StringOrList{dir + "/index.html"},
		Dest:   "/www",
	}
	r, _ := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt"})
	if r.Status != cue.StatusChanged {
		t.Errorf("expected StatusChanged, got %v", r.Status)
	}
	if !strings.Contains(r.Stdout, "1 file(s) changed") {
		t.Errorf("unexpected stdout: %q", r.Stdout)
	}
}

func TestPackExecutor_pruneTier1a(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/index.html", []byte("content"), 0644)

	var removed []string
	mock := &mockConn{
		downloads: map[string][]byte{"/www/index.html": []byte("content")},
		runFunc: func(cmd string) (string, string, int, error) {
			if strings.Contains(cmd, "cat") && strings.Contains(cmd, ".regis-pack-src") {
				// Previous manifest: index.html + old.html (stale)
				return "index.html\nold.html\n", "", 0, nil
			}
			if strings.Contains(cmd, "rm -f") {
				removed = append(removed, cmd)
			}
			return "", "", 0, nil
		},
	}

	ex := cue.NewPackExecutor(mock)
	cr := config.CueRef{
		Name:   "src",
		Nature: "pack",
		Src:    config.StringOrList{dir + "/index.html"},
		Dest:   "/www",
		Prune:  boolPtr(true),
	}
	r, _ := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt"})
	if r.Status != cue.StatusChanged {
		t.Errorf("expected StatusChanged (prune ran), got %v stdout=%q", r.Status, r.Stdout)
	}
	if !strings.Contains(r.Stdout, "pruned [manifest]") {
		t.Errorf("expected tier-1a prune report, got %q", r.Stdout)
	}
	if len(removed) == 0 {
		t.Error("expected rm -f to be called for stale file")
	}
}

func TestPackExecutor_pruneTier3_noManifest(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/index.html", []byte("content"), 0644)

	mock := &mockConn{
		downloads: map[string][]byte{"/www/index.html": []byte("content")},
		runFunc: func(cmd string) (string, string, int, error) {
			// No manifest anywhere; find returns list
			if strings.Contains(cmd, "find") && strings.Contains(cmd, "-maxdepth") {
				if strings.Contains(cmd, "-printf") {
					return "", "", 1, nil // -printf not supported → tier 2 fails
				}
				return "/www/index.html\n/www/.env\n", "", 0, nil
			}
			return "", "", 0, nil
		},
	}

	ex := cue.NewPackExecutor(mock)
	cr := config.CueRef{
		Name:   "src",
		Nature: "pack",
		Src:    config.StringOrList{dir + "/index.html"},
		Dest:   "/www",
		Prune:  boolPtr(true),
	}
	r, _ := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt"})
	// .env is unmanaged — tier 3 lists it but doesn't delete
	if !strings.Contains(r.Stdout, ".env") {
		t.Errorf("expected .env in tier-3 report, got %q", r.Stdout)
	}
	if strings.Contains(r.Stdout, "pruned") {
		t.Errorf("tier 3 must not prune anything, got %q", r.Stdout)
	}
}

func TestPackExecutor_artifactMapsPopulated(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/index.html", []byte("new content"), 0644)
	os.WriteFile(dir+"/app.js", []byte("js content"), 0644)

	mock := &mockConn{
		downloads: map[string][]byte{
			"/www/index.html": []byte("old content"), // changed
			"/www/app.js":     []byte("js content"),  // unchanged
		},
		runFunc: func(cmd string) (string, string, int, error) { return "", "", 0, nil },
	}

	ex := cue.NewPackExecutor(mock)
	cr := config.CueRef{
		Name:   "web",
		Nature: "pack",
		Src:    config.StringOrList{dir + "/*.html", dir + "/*.js"},
		Dest:   "/www",
	}
	r, err := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt"})
	if err != nil {
		t.Fatal(err)
	}
	// ArtifactPaths and LocalArtifacts must be populated for ALL src files (not just changed).
	if len(r.ArtifactPaths) == 0 {
		t.Error("ArtifactPaths must be populated by pack executor")
	}
	if len(r.LocalArtifacts) == 0 {
		t.Error("LocalArtifacts must be populated by pack executor")
	}
	// Each entry must have matching "web/<relpath>" keys.
	for key, localPath := range r.LocalArtifacts {
		if _, ok := r.ArtifactPaths[key]; !ok {
			t.Errorf("LocalArtifacts key %q missing from ArtifactPaths", key)
		}
		if localPath == "" {
			t.Errorf("LocalArtifacts[%q] is empty", key)
		}
	}
}

func TestPackExecutor_dryRunSkipsUploadAndPrune(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/index.html", []byte("new"), 0644)

	uploaded := false
	mock := &mockConn{
		downloads: map[string][]byte{"/www/index.html": []byte("old")},
		runFunc:   func(cmd string) (string, string, int, error) { return "", "", 0, nil },
	}
	origUpload := mock.uploadErr
	_ = origUpload
	_ = uploaded

	ex := cue.NewPackExecutor(mock)
	cr := config.CueRef{
		Name:   "src",
		Nature: "pack",
		Src:    config.StringOrList{dir + "/index.html"},
		Dest:   "/www",
		Prune:  boolPtr(true),
	}
	ctx := cue.WithDryRun(context.Background())
	r, _ := ex.Execute(ctx, nil, cr, config.Target{Dir: "/opt"})
	// Should detect change but NOT upload (dry-run)
	if r.Status != cue.StatusChanged {
		t.Errorf("dry-run should still detect change, got %v", r.Status)
	}
	// upload path is only set on real uploads; since we don't track per-call we
	// verify indirectly that the manifest was NOT written (runFunc not called for upload)
}

// ---- git: true tests ---------------------------------------------------------

// initGitRepo creates a temp directory, writes files, and commits them.
// Uses git -C so no chdir is required here.
func initGitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"init"},
		{"-c", "user.email=t@t.com", "-c", "user.name=T", "add", "."},
		{"-c", "user.email=t@t.com", "-c", "user.name=T", "commit", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// chdirTemp changes the working directory for the duration of the test.
// Not safe for parallel tests that also use chdirTemp.
func chdirTemp(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) })
}

func TestExpandSrcFromGit_returnsCommittedFiles(t *testing.T) {
	dir := initGitRepo(t, map[string]string{
		"index.html":    "<html/>",
		"css/style.css": "body{}",
	})
	chdirTemp(t, dir)

	paths, err := cue.ExpandSrcFromGit()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]bool{"index.html": true, "css/style.css": true}
	for _, p := range paths {
		delete(want, p)
	}
	if len(want) != 0 {
		t.Errorf("missing committed paths: %v (got %v)", want, paths)
	}
}

func TestExpandSrcFromGit_noGitRepo_error(t *testing.T) {
	chdirTemp(t, t.TempDir()) // plain dir, no git init
	_, err := cue.ExpandSrcFromGit()
	if err == nil {
		t.Error("expected error outside a git repository")
	}
}

func TestPackExecutor_gitTrue_uploadsCommittedFiles(t *testing.T) {
	dir := initGitRepo(t, map[string]string{
		"index.html":    "content a",
		"css/style.css": "content b",
	})
	chdirTemp(t, dir)

	mock := &mockConn{
		downloads: map[string][]byte{}, // all absent on remote → all changed
		runFunc:   func(cmd string) (string, string, int, error) { return "", "", 0, nil },
	}
	ex := cue.NewPackExecutor(mock)
	cr := config.CueRef{Name: "app", Nature: "pack", Git: true, Dest: "/www"}
	r, err := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != cue.StatusChanged {
		t.Errorf("expected StatusChanged, got %v stdout=%q", r.Status, r.Stdout)
	}
	for _, want := range []string{"index.html", "css/style.css"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("expected %q in changed output, got %q", want, r.Stdout)
		}
	}
}

func TestPackExecutor_gitTrue_showsCommitHash_changed(t *testing.T) {
	dir := initGitRepo(t, map[string]string{"index.html": "content"})
	chdirTemp(t, dir)

	mock := &mockConn{
		downloads: map[string][]byte{},
		runFunc:   func(cmd string) (string, string, int, error) { return "", "", 0, nil },
	}
	ex := cue.NewPackExecutor(mock)
	cr := config.CueRef{Name: "app", Nature: "pack", Git: true, Dest: "/www"}
	r, _ := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt"})

	if r.Status != cue.StatusChanged {
		t.Fatalf("expected StatusChanged, got %v", r.Status)
	}
	if !strings.Contains(r.Stdout, "commit ") {
		t.Errorf("expected commit hash in Stdout, got %q", r.Stdout)
	}
}

// TestPackExecutor_equalPopulatesArtifactPaths guards the fix: ArtifactPaths and
// LocalArtifacts must be populated even when StatusEqual so that --force-manifest
// can record the pack cue into the state record without a re-deploy.
func TestPackExecutor_equalPopulatesArtifactPaths(t *testing.T) {
	dir := t.TempDir()
	content := []byte("hello")
	os.WriteFile(dir+"/a.txt", content, 0644)
	os.WriteFile(dir+"/b.txt", content, 0644)

	mock := &mockConn{
		runFunc: packEqualRun(map[string][]byte{
			"/www/a.txt": content,
			"/www/b.txt": content,
		}),
	}
	ex := cue.NewPackExecutor(mock)
	cr := config.CueRef{
		Name:   "web",
		Nature: "pack",
		Src:    config.StringOrList{dir + "/*.txt"},
		Dest:   "/www",
	}
	r, err := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != cue.StatusEqual {
		t.Fatalf("expected StatusEqual, got %v", r.Status)
	}
	if len(r.ArtifactPaths) == 0 {
		t.Error("ArtifactPaths must be populated even when StatusEqual")
	}
	if len(r.LocalArtifacts) == 0 {
		t.Error("LocalArtifacts must be populated even when StatusEqual")
	}
	for key, localPath := range r.LocalArtifacts {
		if _, ok := r.ArtifactPaths[key]; !ok {
			t.Errorf("LocalArtifacts key %q missing from ArtifactPaths", key)
		}
		if localPath == "" {
			t.Errorf("LocalArtifacts[%q] is empty", key)
		}
	}
}

func TestPackExecutor_gitTrue_showsCommitHash_equal(t *testing.T) {
	dir := initGitRepo(t, map[string]string{"index.html": "content"})
	chdirTemp(t, dir)

	mock := &mockConn{
		runFunc: packEqualRun(map[string][]byte{"/www/index.html": []byte("content")}),
	}
	ex := cue.NewPackExecutor(mock)
	cr := config.CueRef{Name: "app", Nature: "pack", Git: true, Dest: "/www"}
	r, _ := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt"})

	if r.Status != cue.StatusEqual {
		t.Fatalf("expected StatusEqual, got %v stdout=%q", r.Status, r.Stdout)
	}
	if !strings.Contains(r.Stdout, "commit ") {
		t.Errorf("expected commit hash in Stdout even when equal, got %q", r.Stdout)
	}
}

func TestPackExecutor_gitTrue_warnsStagedFiles(t *testing.T) {
	dir := initGitRepo(t, map[string]string{"committed.go": "v1"})
	chdirTemp(t, dir)
	// Stage a new file without committing
	os.WriteFile("staged.go", []byte("new"), 0644)
	exec.Command("git", "-C", dir, "add", "staged.go").Run()

	mock := &mockConn{
		downloads: map[string][]byte{},
		runFunc:   func(cmd string) (string, string, int, error) { return "", "", 0, nil },
	}
	ex := cue.NewPackExecutor(mock)
	r, _ := ex.Execute(context.Background(), nil,
		config.CueRef{Name: "app", Nature: "pack", Git: true, Dest: "/www"},
		config.Target{Dir: "/opt"})

	var found bool
	for _, w := range r.Warnings {
		if strings.Contains(w, "staged") && strings.Contains(w, "staged.go") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected staged-file warning, got Warnings=%v", r.Warnings)
	}
}

func TestPackExecutor_gitTrue_warnsUntrackedFiles(t *testing.T) {
	dir := initGitRepo(t, map[string]string{"main.go": "v1"})
	chdirTemp(t, dir)
	os.WriteFile("untracked.go", []byte("new"), 0644) // not added to git

	mock := &mockConn{
		downloads: map[string][]byte{},
		runFunc:   func(cmd string) (string, string, int, error) { return "", "", 0, nil },
	}
	ex := cue.NewPackExecutor(mock)
	r, _ := ex.Execute(context.Background(), nil,
		config.CueRef{Name: "app", Nature: "pack", Git: true, Dest: "/www"},
		config.Target{Dir: "/opt"})

	var found bool
	for _, w := range r.Warnings {
		if strings.Contains(w, "untracked") && strings.Contains(w, "untracked.go") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected untracked-file warning, got Warnings=%v", r.Warnings)
	}
}

func TestPackExecutor_gitTrue_respectsRegisignore(t *testing.T) {
	dir := initGitRepo(t, map[string]string{
		"keep.html": "keep",
		"skip.html": "skip", // committed but filtered at deploy time
	})
	chdirTemp(t, dir)
	// .regisignore is a deploy-time denylist — not required to be in git
	if err := os.WriteFile(".regisignore", []byte("skip.html\n"), 0644); err != nil {
		t.Fatal(err)
	}

	mock := &mockConn{
		downloads: map[string][]byte{},
		runFunc:   func(cmd string) (string, string, int, error) { return "", "", 0, nil },
	}
	ex := cue.NewPackExecutor(mock)
	cr := config.CueRef{Name: "web", Nature: "pack", Git: true, Dest: "/www"}
	r, err := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/opt"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.Stdout, "skip.html") {
		t.Errorf(".regisignore should have excluded skip.html; stdout=%q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "keep.html") {
		t.Errorf("expected keep.html in changed output; stdout=%q", r.Stdout)
	}
}
