// internal/cue/binary.go
// doc:nature binary
// Uploads a compiled executable. Change detection via MD5.
// Atomic upload: copies to <dest>.new, then mv. Direction: local→remote.
// rollback: true — restores the previous binary from the local release snapshot.
package cue

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"git.disroot.org/jmy/regis/internal/config"
)

// joinRemotePath resolves dest onto dir using the remote host's path separator.
// conn.PathSep() provides the cached separator detected once at Dial time.
// Falls back to Unix "/" when conn is nil (tests, local-only phases).
func joinRemotePath(conn SSHConn, dir, dest string) string {
	sep := "/"
	if conn != nil {
		sep = conn.PathSep()
	}
	if sep == `\` {
		return joinWindowsRemote(dir, dest)
	}
	// Unix: always forward slashes regardless of where regis runs.
	if path.IsAbs(dest) {
		return dest
	}
	return path.Join(dir, dest)
}

// joinWindowsRemote joins a Windows remote path using backslash.
func joinWindowsRemote(dir, dest string) string {
	// dest is absolute if it has a drive letter or is rooted with \ or /
	if (len(dest) >= 2 && dest[1] == ':') ||
		strings.HasPrefix(dest, `\`) || strings.HasPrefix(dest, "/") {
		return strings.ReplaceAll(dest, "/", `\`)
	}
	return strings.TrimRight(dir, `/\`) + `\` + dest
}

// BinaryExecutor handles nature: binary cues (MD5 compare + upload).
type BinaryExecutor struct{ conn SSHConn }

// NewBinaryExecutor creates a BinaryExecutor. conn is used for MD5 + Upload calls.
func NewBinaryExecutor(conn SSHConn) *BinaryExecutor { return &BinaryExecutor{conn: conn} }

// Execute compares local MD5 to remote, uploads if different.
func (e *BinaryExecutor) Execute(ctx context.Context, conn SSHConn, cr config.CueRef, target config.Target) (Result, error) {
	start := time.Now()
	r := Result{
		CueName:        cr.Name,
		Nature:         "binary",
		AffectsRelease: true,
	}

	if e.conn == nil {
		r.Status = StatusFailed
		r.Err = fmt.Errorf("binary %q: no SSH connection available", cr.Name)
		r.Elapsed = time.Since(start)
		return r, nil
	}

	localPath := string(cr.Src[0])
	remotePath := joinRemotePath(e.conn, target.Dir, cr.Dest)

	localMD5, err := LocalMD5(localPath)
	if err != nil {
		r.Status = StatusFailed
		r.Err = fmt.Errorf("local MD5 %s: %w", localPath, err)
		return r, nil
	}

	// Skip the remote MD5 when we know the file doesn't exist on the target
	// (populated by rdiff's bulk find — avoids one SFTP error per missing binary).
	var remoteMD5 string
	if RemoteFilesKnown(ctx) && !RemoteFileExists(ctx, remotePath) {
		remoteMD5 = "" // file absent; falls through to "changed" below
	} else {
		var err error
		remoteMD5, err = e.conn.MD5(remotePath)
		if err == nil && localMD5 == remoteMD5 {
			r.Status = StatusEqual
			r.Elapsed = time.Since(start)
			return r, nil
		}
	}

	// MD5 differs — capture paths, timestamps and checksums for display/drift detection.
	r.LocalPath = localPath
	r.RemotePath = remotePath
	r.LocalMD5 = localMD5
	r.RemoteMD5 = remoteMD5
	if fi, statErr := os.Stat(localPath); statErr == nil {
		r.LocalMtime = fi.ModTime()
	}
	r.RemoteMtime, _ = e.conn.Stat(remotePath)

	// Check manifest drift: remote file was modified after last deploy.
	if m := ManifestFrom(ctx); m != nil {
		if expected, ok := m.Checksums[cr.Name]; ok && expected != "" {
			r.ManifestDrift = remoteMD5 != expected
			r.ManifestChecksum = expected
		}
	}

	// Dry-run: change detected, but skip upload
	if IsDryRun(ctx) {
		r.Status = StatusChanged
		r.Elapsed = time.Since(start)
		return r, nil
	}

	// Upload
	useSudo := cr.Sudo || target.Sudo
	if err := e.conn.Upload(localPath, remotePath, 0755, useSudo); err != nil {
		r.Status = StatusFailed
		r.Err = fmt.Errorf("upload %s: %w", localPath, err)
		r.Elapsed = time.Since(start)
		return r, nil
	}

	r.Status = StatusChanged
	r.Elapsed = time.Since(start)
	if cr.Post.Cmd != "" {
		r.PostActions = []PostAction{{Cmd: cr.Post.Cmd, Sudo: cr.Post.Sudo || target.Sudo}}
	}
	return r, nil
}
