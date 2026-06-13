// internal/cue/render.go
// doc:nature render
// Runs a shell command locally to produce rendered output.
// For a single file: dest has no trailing slash.
//   Preferred: shell writes to stdout; regis captures and saves it. No redirect needed.
//   Alternative: shell writes to $ARTIFACT_PATH (injected env var) directly.
// For a folder:      dest ends with / — shell must write files into $ARTIFACT_PATH/ directory.
// local_dest: persistent local path for rendered output; populated by regis fetch.
// reverse: shell run by regis fetch after writing local_dest; $ARTIFACT_PATH = downloaded path.
// Change detection: text diff for UTF-8 content, MD5 for binary.
// prune: true (folder mode only) deletes remote files absent from the rendered output.
// Always runs — even during rdiff — so comparisons reflect freshly rendered content.
// Direction: local (rendered) → remote.
// restore: true — re-deploy previous version from git at the recorded state ref.
package cue

import (
	"bytes"
	"context"
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"git.disroot.org/jmy/regis/internal/config"
)

// RenderExecutor handles nature: render cues.
type RenderExecutor struct{ conn SSHConn }

// NewRenderExecutor creates a RenderExecutor. conn is used for Download/Upload calls.
func NewRenderExecutor(conn SSHConn) *RenderExecutor { return &RenderExecutor{conn: conn} }

// Execute runs the render shell, reads $REGIS_DEST, diffs against remote, uploads if changed.
func (e *RenderExecutor) Execute(ctx context.Context, conn SSHConn, cr config.CueRef, target config.Target) (Result, error) {
	start := time.Now()
	r := Result{
		CueName:        cr.Name,
		Nature:         "render",
		AffectsState: true,
	}

	isFolder := strings.HasSuffix(cr.Dest, "/")

	// Determine artifact path: use local_dest when set (persistent), otherwise a temp file/dir.
	var artifactPath string
	if cr.LocalDest != "" {
		if err := os.MkdirAll(filepath.Dir(cr.LocalDest), 0755); err != nil {
			r.Status = StatusFailed
			r.Err = fmt.Errorf("create local_dest parent: %w", err)
			r.Elapsed = time.Since(start)
			return r, nil
		}
		artifactPath = cr.LocalDest
	} else if isFolder {
		dir, err := os.MkdirTemp("", "regis-render-*")
		if err != nil {
			r.Status = StatusFailed
			r.Err = fmt.Errorf("create temp dir: %w", err)
			r.Elapsed = time.Since(start)
			return r, nil
		}
		defer os.RemoveAll(dir)
		artifactPath = dir + string(os.PathSeparator)
	} else {
		f, err := os.CreateTemp("", "regis-render-*")
		if err != nil {
			r.Status = StatusFailed
			r.Err = fmt.Errorf("create temp file: %w", err)
			r.Elapsed = time.Since(start)
			return r, nil
		}
		f.Close()
		artifactPath = f.Name()
		defer os.Remove(artifactPath)
	}

	// Run shell with ARTIFACT_PATH injected.
	env := make(map[string]string, len(cr.Env)+1)
	for k, v := range cr.Env {
		env[k] = v
	}
	env["ARTIFACT_PATH"] = artifactPath

	stdout, stderr, exitCode, runErr := runLocal(ctx, cr.Shell, env)
	r.Stdout = stdout
	r.Stderr = stderr

	if runErr != nil {
		r.Status = StatusFailed
		r.Err = runErr
		r.Elapsed = time.Since(start)
		return r, nil
	}
	if exitCode != 0 {
		r.Status = StatusFailed
		r.Err = fmt.Errorf("exit %d: %s", exitCode, strings.TrimSpace(stderr))
		r.Elapsed = time.Since(start)
		return r, nil
	}

	if isFolder {
		return e.executeFolder(ctx, cr, target, r, artifactPath, start)
	}

	// Single-file mode: if the shell wrote nothing to artifactPath but produced stdout,
	// use stdout as the artifact (shell: without > "$ARTIFACT_PATH" redirect).
	// This is simpler and cross-platform — no shell redirect quoting issues.
	if stdout != "" {
		if fi, statErr := os.Stat(artifactPath); statErr != nil || fi.Size() == 0 {
			_ = os.WriteFile(artifactPath, []byte(stdout), 0644)
		}
	}

	return e.executeFile(ctx, cr, target, r, artifactPath, start)
}

// executeFile handles single-file render: compare temp file vs remote, upload if changed.
func (e *RenderExecutor) executeFile(ctx context.Context, cr config.CueRef, target config.Target, r Result, tmpPath string, start time.Time) (Result, error) {
	if e.conn == nil {
		r.Status = StatusFailed
		r.Err = fmt.Errorf("render %q: no SSH connection available", cr.Name)
		r.Elapsed = time.Since(start)
		return r, nil
	}

	localData, err := os.ReadFile(tmpPath)
	if err != nil {
		r.Status = StatusFailed
		r.Err = fmt.Errorf("read rendered output: %w", err)
		r.Elapsed = time.Since(start)
		return r, nil
	}

	remotePath := JoinRemotePath(e.conn, target.Dir, cr.Dest)

	// Compare by remote hash — avoids downloading the rendered file.
	// Rendered output is generated; a text diff of remote vs. local is not meaningful.
	lmd5 := localHashBytes(localData)
	r.LocalHash = lmd5
	remoteMissing := RemoteFilesKnown(ctx) && !RemoteFileExists(ctx, remotePath)
	var remoteHash string
	if !remoteMissing {
		remoteHash, _ = HashRemote(e.conn, remotePath)
	}
	if !remoteMissing && remoteHash != "" && lmd5 == remoteHash {
		r.Status = StatusEqual
		r.Elapsed = time.Since(start)
		return r, nil
	}
	if remoteMissing {
		r.Diff = fmt.Sprintf("new file  rendered:%s", truncateHash(lmd5))
	} else {
		r.Diff = fmt.Sprintf("changed  remote:%s  rendered:%s", truncateHash(remoteHash), truncateHash(lmd5))
	}

	if IsDryRun(ctx) {
		r.Status = StatusChanged
		r.Elapsed = time.Since(start)
		return r, nil
	}

	useSudo := cr.Sudo || target.Sudo
	if err := e.conn.UploadBytes(localData, remotePath, 0644, useSudo); err != nil {
		r.Status = StatusFailed
		r.Err = fmt.Errorf("upload %s: %w", cr.Dest, err)
		r.Elapsed = time.Since(start)
		return r, nil
	}

	r.Status = StatusChanged
	r.Size = int64(len(localData))
	if cr.Post.Cmd != "" {
		r.PostActions = []PostAction{{Cmd: cr.Post.Cmd, Sudo: cr.Post.Sudo || target.Sudo}}
	}
	r.Elapsed = time.Since(start)
	return r, nil
}

// executeFolder handles folder render: walk temp dir, compare/upload each file,
// optionally prune remote-only files.
func (e *RenderExecutor) executeFolder(ctx context.Context, cr config.CueRef, target config.Target, r Result, tmpDir string, start time.Time) (Result, error) {
	if e.conn == nil {
		r.Status = StatusFailed
		r.Err = fmt.Errorf("render %q: no SSH connection available", cr.Name)
		r.Elapsed = time.Since(start)
		return r, nil
	}

	// Ensure remoteDest has a trailing separator for folder mode path joins.
	// JoinRemotePath strips the trailing slash that signals folder mode.
	remoteDest := JoinRemotePath(e.conn, target.Dir, cr.Dest)
	sep := e.conn.PathSep()
	if !strings.HasSuffix(remoteDest, sep) {
		remoteDest += sep
	}
	dryRun := IsDryRun(ctx)
	useSudo := cr.Sudo || target.Sudo

	// Walk temp dir and collect all generated files.
	type localFile struct {
		absPath string
		relPath string // forward-slash relative path within tmpDir
	}
	var localFiles []localFile
	stripDir := strings.TrimRight(tmpDir, `/\`)
	err := filepath.Walk(strings.TrimRight(tmpDir, `/\`), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel := filepath.ToSlash(strings.TrimPrefix(path, stripDir+string(os.PathSeparator)))
		localFiles = append(localFiles, localFile{absPath: path, relPath: rel})
		return nil
	})
	if err != nil {
		r.Status = StatusFailed
		r.Err = fmt.Errorf("walk rendered output: %w", err)
		r.Elapsed = time.Since(start)
		return r, nil
	}

	// Bulk-prefetch remote hashes for all rendered files — avoids per-file downloads.
	var remotePaths []string
	for _, lf := range localFiles {
		rp := remoteDest + lf.relPath
		if !RemoteFilesKnown(ctx) || RemoteFileExists(ctx, rp) {
			remotePaths = append(remotePaths, rp)
		}
	}
	remoteHashes := BulkHashRemote(e.conn, remotePaths)

	var changedNames []string
	var totalSize int64
	var diffBuf strings.Builder
	progressFn := FileProgressFrom(ctx)
	localHashes := make(map[string]string, len(localFiles)) // relPath → lmd5 for composite hash

	localRelPaths := make(map[string]bool, len(localFiles))
	for i, lf := range localFiles {
		localRelPaths[lf.relPath] = true
		remoteFilePath := remoteDest + lf.relPath

		localData, err := os.ReadFile(lf.absPath)
		if err != nil {
			r.Status = StatusFailed
			r.Err = fmt.Errorf("read %s: %w", lf.relPath, err)
			r.Elapsed = time.Since(start)
			return r, nil
		}

		lmd5 := localHashBytes(localData)
		localHashes[lf.relPath] = lmd5
		remoteHash := remoteHashes[remoteFilePath]
		remoteMissing := RemoteFilesKnown(ctx) && !RemoteFileExists(ctx, remoteFilePath)

		var fileChanged bool
		if !remoteMissing && remoteHash != "" && lmd5 == remoteHash {
			// equal
		} else {
			fileChanged = true
			if remoteMissing || remoteHash == "" {
				fmt.Fprintf(&diffBuf, "new %s  rendered:%s\n", lf.relPath, truncateHash(lmd5))
			} else {
				fmt.Fprintf(&diffBuf, "changed %s  remote:%s  rendered:%s\n",
					lf.relPath, truncateHash(remoteHash), truncateHash(lmd5))
			}
		}

		if progressFn != nil {
			progressFn(cr.Name, i+1, len(localFiles))
		}

		if !fileChanged {
			continue
		}
		changedNames = append(changedNames, lf.relPath)

		if !dryRun {
			if err := e.conn.UploadBytes(localData, remoteFilePath, 0644, useSudo); err != nil {
				r.Status = StatusFailed
				r.Err = fmt.Errorf("upload %s: %w", lf.relPath, err)
				r.Elapsed = time.Since(start)
				return r, nil
			}
			totalSize += int64(len(localData))
		}
	}

	// Prune remote-only files.
	var prunedNames []string
	if cr.Prune != nil && *cr.Prune && !dryRun {
		findCmd := "find " + shellQuote(remoteDest) + " -type f"
		findOut, _, _, _ := e.conn.Run(findCmd)
		for _, line := range strings.Split(strings.TrimSpace(findOut), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			// Convert remote absolute path to relative
			rel := strings.TrimPrefix(line, remoteDest)
			rel = strings.TrimPrefix(rel, "/")
			if !localRelPaths[rel] {
				rmCmd := "rm -f " + shellQuote(line)
				if useSudo {
					e.conn.RunSudo(rmCmd)
				} else {
					e.conn.Run(rmCmd)
				}
				prunedNames = append(prunedNames, rel)
			}
		}
	}

	// Compute composite hash and per-file artifact maps (both StatusEqual and StatusChanged).
	r.LocalHash = multiFileHash(localHashes)
	r.LocalFileHashes = make(map[string]string, len(localFiles))
	r.ArtifactPaths = make(map[string]string, len(localFiles))
	r.LocalArtifacts = make(map[string]string, len(localFiles))
	for _, lf := range localFiles {
		key := cr.Name + "/" + lf.relPath
		r.LocalFileHashes[key] = localHashes[lf.relPath]
		r.ArtifactPaths[key] = remoteDest + lf.relPath
		r.LocalArtifacts[key] = lf.absPath
	}

	// Aggregate result.
	if len(changedNames) == 0 && len(prunedNames) == 0 {
		r.Status = StatusEqual
		r.FileTotal = len(localRelPaths)
		r.FileChanged = 0
		r.Elapsed = time.Since(start)
		return r, nil
	}

	r.Status = StatusChanged
	r.Size = totalSize
	r.FileTotal = len(localRelPaths)
	r.FileChanged = len(changedNames)
	r.Diff = strings.TrimRight(diffBuf.String(), "\n")

	// Build Stdout summary shown by AppendDetails in -v mode.
	var summary strings.Builder
	if len(changedNames) > 0 {
		fmt.Fprintf(&summary, "%d file(s) changed: %s", len(changedNames), strings.Join(changedNames, ", "))
	}
	if len(prunedNames) > 0 {
		if summary.Len() > 0 {
			summary.WriteString("\n")
		}
		fmt.Fprintf(&summary, "%d file(s) pruned: %s", len(prunedNames), strings.Join(prunedNames, ", "))
	}
	r.Stdout = summary.String()

	if !dryRun && cr.Post.Cmd != "" {
		r.PostActions = []PostAction{{Cmd: cr.Post.Cmd, Sudo: cr.Post.Sudo || target.Sudo}}
	}
	r.Elapsed = time.Since(start)
	return r, nil
}

// isBinaryContent reports whether data contains a null byte — a reliable binary heuristic.
func isBinaryContent(data []byte) bool {
	return bytes.IndexByte(data, 0) >= 0
}

// localHashBytes returns the hex hash of data.
func localHashBytes(data []byte) string {
	h := md5.Sum(data)
	return fmt.Sprintf("%x", h)
}

// truncateHash returns the first 12 hex chars of a hash string.
func truncateHash(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// shellQuote wraps path in single quotes, escaping any embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

