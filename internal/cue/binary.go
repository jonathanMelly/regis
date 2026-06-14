// internal/cue/binary.go
// doc:nature binary
// Uploads a compiled executable. Change detection: mtime+size fast path, hash fallback.
// Atomic upload: copies to <dest>.new, then mv. Direction: local→remote.
// compensation: file state is not automatically restored — use `regis state hint` for recovery guidance.
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

// JoinRemotePath resolves dest onto dir using the remote host's path separator.
// conn.PathSep() provides the cached separator detected once at Dial time.
// Falls back to Unix "/" when conn is nil (tests, local-only phases).
func JoinRemotePath(conn SSHConn, dir, dest string) string {
	sep := "/"
	if conn != nil {
		sep = conn.PathSep()
	}
	if sep == `\` {
		return joinWindowsRemote(dir, dest)
	}
	if path.IsAbs(dest) {
		return dest
	}
	return path.Join(dir, dest)
}

// joinWindowsRemote joins a Windows remote path using backslash.
func joinWindowsRemote(dir, dest string) string {
	if (len(dest) >= 2 && dest[1] == ':') ||
		strings.HasPrefix(dest, `\`) || strings.HasPrefix(dest, "/") {
		return strings.ReplaceAll(dest, "/", `\`)
	}
	return strings.TrimRight(dir, `/\`) + `\` + dest
}

// BinaryExecutor handles nature: binary cues (mtime+size fast compare, hash fallback, upload).
type BinaryExecutor struct{ conn SSHConn }

// NewBinaryExecutor creates a BinaryExecutor.
func NewBinaryExecutor(conn SSHConn) *BinaryExecutor { return &BinaryExecutor{conn: conn} }

// Execute compares local file to remote (mtime+size fast path, hash fallback), uploads if different.
func (e *BinaryExecutor) Execute(ctx context.Context, _ SSHConn, cr config.CueRef, target config.Target) (Result, error) {
	start := time.Now()
	r := Result{
		CueName:        cr.Name,
		Nature:         "binary",
		AffectsState: true,
	}

	if e.conn == nil {
		r.Status = StatusFailed
		r.Err = fmt.Errorf("binary %q: no SSH connection available", cr.Name)
		r.Elapsed = time.Since(start)
		return r, nil
	}

	localPath := cr.Src[0]
	remotePath := JoinRemotePath(e.conn, target.Dir, cr.Dest)

	var localMtime time.Time
	var localSize int64 = -1
	if fi, err := os.Stat(localPath); err == nil {
		localMtime = fi.ModTime()
		localSize = fi.Size()
	}

	fileKnownMissing := RemoteFilesKnown(ctx) && !RemoteFileExists(ctx, remotePath)

	// ── fast path: look up bulk-prefetched stats ───────────────────────────────
	if stats := RemoteStatsFrom(ctx); stats != nil {
		rs, found := stats[remotePath]
		if !found || rs.Size < 0 {
			// file absent → treat as changed
			return e.applyOrChanged(ctx, cr, target, r, start, localPath, remotePath, localMtime, "", "")
		}
		if rs.Hash != "" {
			// hash pre-computed (mtime/size differed); compare with local
			localHash, err := LocalHash(localPath)
			if err != nil {
				r.Status = StatusFailed
				r.Err = fmt.Errorf("local hash %s: %w", localPath, err)
				r.Elapsed = time.Since(start)
				return r, nil
			}
			if rs.Hash == localHash {
				if (!IsCheckOnly(ctx) || IsUpdateMtime(ctx)) && !localMtime.IsZero() {
					// Sync remote mtime so the next rdiff/run can skip the hash check via the fast mtime+size path.
					SetRemoteMtime(e.conn, remotePath, localMtime)
				}
				r.Status = StatusEqual
				r.Elapsed = time.Since(start)
				return r, nil
			}
			r.LocalHash = localHash
			r.RemoteHash = rs.Hash
			r.LocalMtime = localMtime
			r.RemoteMtime = rs.Mtime
			return e.applyOrChanged(ctx, cr, target, r, start, localPath, remotePath, localMtime, localHash, rs.Hash)
		}
		// mtime+size matched local → equal
		r.Status = StatusEqual
		r.Elapsed = time.Since(start)
		return r, nil
	}

	// ── fallback: individual SSH calls ─────────────────────────────────────────
	var remoteMtime time.Time
	if !fileKnownMissing && localSize >= 0 {
		var remoteSize int64
		remoteMtime, remoteSize = StatRemote(e.conn, remotePath)
		if !remoteMtime.IsZero() && remoteSize >= 0 &&
			remoteMtime.Unix() == localMtime.Unix() && remoteSize == localSize {
			r.Status = StatusEqual
			r.Elapsed = time.Since(start)
			return r, nil
		}
	}

	localHash, err := LocalHash(localPath)
	if err != nil {
		r.Status = StatusFailed
		r.Err = fmt.Errorf("local hash %s: %w", localPath, err)
		r.Elapsed = time.Since(start)
		return r, nil
	}

	var remoteHash string
	if !fileKnownMissing {
		remoteHash, err = HashRemote(e.conn, remotePath)
		if err == nil && localHash == remoteHash {
			if (!IsCheckOnly(ctx) || IsUpdateMtime(ctx)) && !localMtime.IsZero() {
				// Sync remote mtime so the next rdiff/run can skip the hash check via the fast mtime+size path.
				SetRemoteMtime(e.conn, remotePath, localMtime)
			}
			r.Status = StatusEqual
			r.Elapsed = time.Since(start)
			return r, nil
		}
	}

	r.LocalMtime = localMtime
	r.RemoteMtime = remoteMtime
	return e.applyOrChanged(ctx, cr, target, r, start, localPath, remotePath, localMtime, localHash, remoteHash)
}

// applyOrChanged sets manifest drift info and either uploads (real run) or returns StatusChanged (check-only).
func (e *BinaryExecutor) applyOrChanged(
	ctx context.Context, cr config.CueRef, target config.Target, r Result,
	start time.Time, localPath, remotePath string, localMtime time.Time,
	localHash, remoteHash string,
) (Result, error) {
	r.LocalHash = localHash
	r.RemoteHash = remoteHash
	r.LocalPath = localPath
	r.RemotePath = remotePath

	if m := ManifestFrom(ctx); m != nil {
		if expected, ok := m.Hashes[cr.Name]; ok && expected != "" {
			r.ManifestDrift = remoteHash != expected
			r.ManifestHash = expected
		}
	}

	if IsCheckOnly(ctx) {
		r.Status = StatusChanged
		r.Elapsed = time.Since(start)
		return r, nil
	}

	useSudo := cr.Sudo || target.Sudo
	if err := e.conn.Upload(localPath, remotePath, 0755, useSudo); err != nil {
		r.Status = StatusFailed
		r.Err = fmt.Errorf("upload %s: %w", localPath, err)
		r.Elapsed = time.Since(start)
		return r, nil
	}

	// Enforce mtime replication so next run can use the fast path.
	if !localMtime.IsZero() {
		SetRemoteMtime(e.conn, remotePath, localMtime)
	}

	r.Status = StatusChanged
	r.Elapsed = time.Since(start)
	if cr.Post.Cmd != "" {
		r.PostActions = []PostAction{{Cmd: cr.Post.Cmd, Sudo: cr.Post.Sudo || target.Sudo}}
	}
	return r, nil
}
