// internal/cue/binary.go
// doc:nature binary
// Uploads a compiled executable. Direction: local→remote.
// Change detection: mtime+size fast path, hash fallback.
// Atomic upload: copies to <dest>.new, then mv.
// compensation: file state is not automatically restored — use `regis state hint` for recovery guidance.
//
// managed_by: combines binary upload + service registration in one cue (preferred over a separate service cue
// for custom binaries). Scalar or struct form:
//
// ```yaml
// # scalar — crontab or systemd
// - name: app
//   src: bin/app
//   dest: app
//   managed_by: crontab          # nature: binary inferred from src: + managed_by:
//
// # struct — systemd with unit file
// - name: app
//   src: bin/app
//   dest: app
//   managed_by:
//     manager: systemd
//     service_file: deploy/app.service   # uploaded to /etc/systemd/system/app.service
//     sudo: true
//     restart: false             # skip restart here — handled by scenario post:
// ```
//
// After upload the service registration check runs and queues deploy:<name> when not yet installed.
// post: restart:<name> / reload:<name> shorthands resolve managed_by: cues the same as nature: service cues.
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
// When cr.ManagedBy is set, it also checks and registers the service after upload.
type BinaryExecutor struct {
	conn SSHConn
	env  map[string]string
}

// NewBinaryExecutor creates a BinaryExecutor.
// Pass the target's env map (from config.BuildEnvForTarget) to enable ${VAR} expansion
// in service unit files when managed_by: includes a service_file:.
func NewBinaryExecutor(conn SSHConn, env ...map[string]string) *BinaryExecutor {
	e := &BinaryExecutor{conn: conn}
	if len(env) > 0 {
		e.env = env[0]
	}
	return e
}

// Execute compares local file to remote, uploads if different, then handles service
// registration when managed_by: is set.
func (e *BinaryExecutor) Execute(ctx context.Context, conn SSHConn, cr config.CueRef, target config.Target) (Result, error) {
	r, err := e.executeBinary(ctx, cr, target)
	if err != nil || r.Status == StatusFailed {
		return r, err
	}
	if cr.ManagedBy != nil {
		svcCR := config.ProjectManagedBy(cr)
		svcExec := NewServiceExecutor(e.conn, e.env)
		svcResult, svcErr := svcExec.Execute(ctx, e.conn, svcCR, target)
		if svcErr != nil {
			return r, svcErr
		}
		if svcResult.Status == StatusFailed {
			r.Status = StatusFailed
			r.Err = svcResult.Err
			return r, nil
		}
		if svcResult.Status == StatusChanged && r.Status == StatusEqual {
			r.Status = StatusChanged
		}
		if svcResult.Diff != "" {
			r.Diff = svcResult.Diff
		}
		// Auto-restart: queue restart:<svc> when something changed and restart not opted out.
		// Order: service registration (deploy:) → restart: → custom post:.
		var restartActions []PostAction
		if r.Status == StatusChanged && !IsCheckOnly(ctx) {
			if cr.ManagedBy.Restart == nil || *cr.ManagedBy.Restart {
				restartActions = []PostAction{{Cmd: "restart:" + serviceName(svcCR), Sudo: svcCR.Sudo}}
			}
		}
		combined := make([]PostAction, 0, len(svcResult.PostActions)+len(restartActions)+len(r.PostActions))
		combined = append(combined, svcResult.PostActions...)
		combined = append(combined, restartActions...)
		combined = append(combined, r.PostActions...)
		r.PostActions = combined
	}
	return r, nil
}

// executeBinary compares local file to remote (mtime+size fast path, hash fallback), uploads if different.
func (e *BinaryExecutor) executeBinary(ctx context.Context, cr config.CueRef, target config.Target) (Result, error) {
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
