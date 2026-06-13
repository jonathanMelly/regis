// internal/cue/secret.go
// doc:nature secret
// Uploads an env file. Values are masked in all output. preserve: lists keys never overwritten.
// Direction: local→remote.
// restore: true — re-deploy previous version from git at the recorded state ref.
package cue

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"time"

	"git.disroot.org/jmy/regis/internal/config"
)

// SecretExecutor handles nature: secret cues (masked merge).
type SecretExecutor struct{ conn SSHConn }

// NewSecretExecutor creates a SecretExecutor.
func NewSecretExecutor(conn SSHConn) *SecretExecutor { return &SecretExecutor{conn: conn} }

// Execute downloads remote .env, merges with local (preserving listed keys), uploads merged content.
func (e *SecretExecutor) Execute(ctx context.Context, conn SSHConn, cr config.CueRef, target config.Target) (Result, error) {
	start := time.Now()
	r := Result{CueName: cr.Name, Nature: "secret", AffectsState: true}

	if e.conn == nil {
		r.Status = StatusFailed
		r.Err = fmt.Errorf("secret %q: no SSH connection available", cr.Name)
		r.Elapsed = time.Since(start)
		return r, nil
	}

	localPath := string(cr.Src[0])
	remotePath := JoinRemotePath(e.conn, target.Dir, cr.Dest)
	preserve := []string(cr.Preserve)

	localData, err := os.ReadFile(localPath)
	if err != nil {
		if os.IsNotExist(err) {
			r.Status = StatusSkipped
			r.Stdout = fmt.Sprintf("local file %s not found — skipping (remote unchanged)", localPath)
			r.Elapsed = time.Since(start)
			return r, nil
		}
		r.Status = StatusFailed
		r.Err = fmt.Errorf("read %s: %w", localPath, err)
		return r, nil
	}

	// Skip the download when we know the file doesn't exist (first deploy to empty target).
	var remoteData []byte
	if !RemoteFilesKnown(ctx) || RemoteFileExists(ctx, remotePath) {
		remoteData, _ = e.conn.Download(remotePath)
	}

	diff, merged := MergeSecrets(string(localData), string(remoteData), preserve)

	_, changed := SecretDiff(string(localData), string(remoteData), preserve)
	if !changed {
		r.Status = StatusEqual
		r.Elapsed = time.Since(start)
		return r, nil
	}
	r.Diff = diff

	// Parse file mode (octal string, e.g. "600")
	mode := fs.FileMode(0600)
	if cr.Mode != "" {
		if parsed, err := strconv.ParseUint(cr.Mode, 8, 32); err == nil {
			mode = fs.FileMode(parsed)
		}
	}

	// Dry-run: diff computed, skip upload
	if IsCheckOnly(ctx) {
		r.Status = StatusChanged
		r.Elapsed = time.Since(start)
		return r, nil
	}

	useSudo := cr.Sudo || target.Sudo
	if err := e.conn.UploadBytes([]byte(merged), remotePath, mode, useSudo); err != nil {
		r.Status = StatusFailed
		r.Err = fmt.Errorf("upload secret %s: %w", remotePath, err)
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
