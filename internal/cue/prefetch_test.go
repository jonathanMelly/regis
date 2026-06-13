// internal/cue/prefetch_test.go
package cue_test

import (
	"crypto/md5"
	"fmt"
	"os"
	"strings"
	"testing"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
	"context"
)

// ── BulkHashRemote batching ───────────────────────────────────────────────────

// TestBulkHashRemote_batches verifies that >BulkBatchSize paths are split into
// multiple md5sum calls and that results from all batches are merged.
func TestBulkHashRemote_batches(t *testing.T) {
	n := cue.BulkBatchSize*2 + 7 // crosses two batch boundaries
	contents := make(map[string][]byte, n)
	for i := 0; i < n; i++ {
		contents[fmt.Sprintf("/app/file%04d.dat", i)] = []byte(fmt.Sprintf("content-%d", i))
	}

	var callCount int
	mock := &mockConn{runFunc: func(cmd string) (string, string, int, error) {
		if !strings.Contains(cmd, "md5sum") {
			return "", "", 0, nil
		}
		callCount++
		var sb strings.Builder
		for path, data := range contents {
			if strings.Contains(cmd, "'"+path+"'") {
				h := md5.Sum(data)
				fmt.Fprintf(&sb, "%x  %s\n", h, path)
			}
		}
		return sb.String(), "", 0, nil
	}}

	// Use the pack executor as an integration vehicle: create temp files and check equal.
	dir := t.TempDir()
	srcs := make([]string, n)
	for i := 0; i < n; i++ {
		p := fmt.Sprintf("%s/file%04d.dat", dir, i)
		os.WriteFile(p, contents[fmt.Sprintf("/app/file%04d.dat", i)], 0644)
		srcs[i] = p
	}

	ex := cue.NewPackExecutor(mock)
	cr := config.CueRef{
		Name:   "assets",
		Nature: "pack",
		Src:    srcs,
		Dest:   "/app",
	}
	r, err := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/srv"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != cue.StatusEqual {
		t.Errorf("want StatusEqual for %d matching files, got %v (FileChanged=%d)", n, r.Status, r.FileChanged)
	}

	// Expect ceil(n/BulkBatchSize) md5sum calls (stat returns nothing → all go to hash).
	wantCalls := (n + cue.BulkBatchSize - 1) / cue.BulkBatchSize
	if callCount != wantCalls {
		t.Errorf("want %d md5sum calls for %d paths (batch=%d), got %d",
			wantCalls, n, cue.BulkBatchSize, callCount)
	}
}

// TestBulkHashRemote_emptyResultDoesNotMarkMissing verifies that when BulkHashRemote
// returns zero results (e.g. md5sum unavailable), files are reported as changed but
// NOT as "missing" — preventing false-positive missing reports.
func TestBulkHashRemote_emptyResultDoesNotMarkMissing(t *testing.T) {
	dir := t.TempDir()
	content := []byte("hello")
	os.WriteFile(dir+"/app.js", content, 0644)

	mock := &mockConn{runFunc: func(cmd string) (string, string, int, error) {
		// stat and md5sum both return nothing — simulates unavailable tools.
		return "", "", 0, nil
	}}

	ex := cue.NewPackExecutor(mock)
	cr := config.CueRef{
		Name:   "assets",
		Nature: "pack",
		Src:    config.StringOrList{dir + "/app.js"},
		Dest:   "/var/www",
	}
	r, _ := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/srv"})

	if r.Status != cue.StatusChanged {
		t.Fatalf("want StatusChanged when hash unavailable, got %v", r.Status)
	}
	// Diff must NOT contain "missing" — file existence is unknown, not confirmed absent.
	if strings.Contains(r.Diff, "missing") {
		t.Errorf("diff should not say 'missing' when hash unavailable; got %q", r.Diff)
	}
}

// TestBulkHashRemote_confirmedMissingWhenOtherHashesSucceed verifies that when
// BulkHashRemote returns results for some paths (md5sum is available) but not for
// a specific path, that path is correctly marked as missing.
func TestBulkHashRemote_confirmedMissingWhenOtherHashesSucceed(t *testing.T) {
	dir := t.TempDir()
	present := []byte("present content")
	missing := []byte("missing content")
	os.WriteFile(dir+"/present.js", present, 0644)
	os.WriteFile(dir+"/missing.js", missing, 0644)

	mock := &mockConn{runFunc: func(cmd string) (string, string, int, error) {
		if strings.Contains(cmd, "md5sum") {
			// Return hash for present.js only — missing.js is absent on remote.
			var sb strings.Builder
			if strings.Contains(cmd, "present.js") {
				h := md5.Sum(present)
				fmt.Fprintf(&sb, "%x  /var/www/present.js\n", h)
			}
			return sb.String(), "", 0, nil
		}
		return "", "", 0, nil
	}}

	ex := cue.NewPackExecutor(mock)
	cr := config.CueRef{
		Name:   "assets",
		Nature: "pack",
		Src:    config.StringOrList{dir + "/present.js", dir + "/missing.js"},
		Dest:   "/var/www",
	}
	r, _ := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/srv"})

	if r.Status != cue.StatusChanged {
		t.Fatalf("want StatusChanged (missing.js absent), got %v", r.Status)
	}
	if !strings.Contains(r.Diff, "missing") {
		t.Errorf("diff should mention 'missing' for absent file; got %q", r.Diff)
	}
	// present.js is equal → only missing.js should be in changedNames.
	if r.FileChanged != 1 {
		t.Errorf("want FileChanged=1, got %d", r.FileChanged)
	}
}

// TestBulkStatRemote_batches verifies that BulkStatRemote issues multiple stat calls
// when the path count exceeds BulkBatchSize, and merges all results correctly.
func TestBulkStatRemote_batches(t *testing.T) {
	n := cue.BulkBatchSize + 3
	dir := t.TempDir()

	// Write n local files and record their mtimes.
	type fileInfo struct {
		localPath  string
		remotePath string
		content    []byte
	}
	files := make([]fileInfo, n)
	for i := 0; i < n; i++ {
		content := []byte(fmt.Sprintf("data-%d", i))
		lp := fmt.Sprintf("%s/f%04d.txt", dir, i)
		os.WriteFile(lp, content, 0644)
		files[i] = fileInfo{lp, fmt.Sprintf("/app/f%04d.txt", i), content}
	}

	var statCalls int
	mock := &mockConn{runFunc: func(cmd string) (string, string, int, error) {
		if strings.Contains(cmd, "stat") {
			statCalls++
			var sb strings.Builder
			for _, f := range files {
				if !strings.Contains(cmd, "'"+f.remotePath+"'") {
					continue
				}
				fi, err := os.Stat(f.localPath)
				if err != nil {
					continue
				}
				// Return matching mtime+size → mtime fast path (no hash needed).
				fmt.Fprintf(&sb, "%d %d %s\n", fi.ModTime().Unix(), fi.Size(), f.remotePath)
			}
			return sb.String(), "", 0, nil
		}
		return "", "", 0, nil
	}}

	srcs := make([]string, n)
	for i, f := range files {
		srcs[i] = f.localPath
	}
	ex := cue.NewPackExecutor(mock)
	cr := config.CueRef{
		Name:   "data",
		Nature: "pack",
		Src:    srcs,
		Dest:   "/app",
	}
	r, err := ex.Execute(context.Background(), nil, cr, config.Target{Dir: "/srv"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != cue.StatusEqual {
		t.Errorf("want StatusEqual (mtime fast path), got %v FileChanged=%d", r.Status, r.FileChanged)
	}

	wantStatCalls := (n+cue.BulkBatchSize-1)/cue.BulkBatchSize * 2 // GNU stat + BSD fallback per batch
	if statCalls > wantStatCalls {
		t.Errorf("too many stat calls: got %d, want ≤%d", statCalls, wantStatCalls)
	}
	if statCalls < 2 {
		t.Errorf("expected at least 2 stat calls for %d paths (batch=%d), got %d",
			n, cue.BulkBatchSize, statCalls)
	}
}
