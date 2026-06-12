// internal/cue/config.go
// doc:nature config
// Uploads a text file (nginx conf, YAML, etc.). Change detection via unified text diff shown in output.
// Local file content is rendered with target env vars (${VAR} substitution) before comparison.
// Direction: local→remote.
// rollback: true — restores the previous config file from the local release snapshot.
package cue

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"git.disroot.org/jmy/regis/internal/config"
)

// ConfigExecutor handles nature: config cues (text diff + upload).
// env holds the target's resolved environment variables for template rendering.
type ConfigExecutor struct {
	conn SSHConn
	env  map[string]string
}

// NewConfigExecutor creates a ConfigExecutor.
// Pass the target's env map (from config.BuildEnvForTarget) to enable ${VAR} rendering
// of local file content before comparison and upload. Omit for backward compatibility (no rendering).
func NewConfigExecutor(conn SSHConn, env ...map[string]string) *ConfigExecutor {
	e := &ConfigExecutor{conn: conn}
	if len(env) > 0 {
		e.env = env[0]
	}
	return e
}

// Execute downloads remote content, renders local content with env vars, diffs, uploads if changed.
// Multi-src / glob: uploads each file to dest/, preserving relative paths for glob patterns (tree mode).
func (e *ConfigExecutor) Execute(ctx context.Context, conn SSHConn, cr config.CueRef, target config.Target) (Result, error) {
	start := time.Now()
	r := Result{CueName: cr.Name, Nature: "config", AffectsRelease: true}

	if e.conn == nil {
		r.Status = StatusFailed
		r.Err = fmt.Errorf("config %q: no SSH connection available", cr.Name)
		r.Elapsed = time.Since(start)
		return r, nil
	}

	srcs, err := expandSrcResolved(cr.Src)
	if err != nil {
		r.Status = StatusFailed
		r.Err = err
		return r, nil
	}
	if len(srcs) == 0 {
		r.Status = StatusFailed
		r.Err = fmt.Errorf("config %q: no source files", cr.Name)
		return r, nil
	}

	if len(srcs) > 1 || strings.HasSuffix(cr.Dest, "/") {
		return e.executeMulti(ctx, cr, target, r, srcs, start)
	}
	return e.executeSingle(ctx, cr, target, r, srcs[0].path, start)
}

func (e *ConfigExecutor) executeSingle(ctx context.Context, cr config.CueRef, target config.Target, r Result, localPath string, start time.Time) (Result, error) {
	remotePath := joinRemotePath(e.conn, target.Dir, cr.Dest)

	localData, err := os.ReadFile(localPath)
	if err != nil {
		r.Status = StatusFailed
		r.Err = fmt.Errorf("read %s: %w", localPath, err)
		return r, nil
	}

	localContent := string(localData)
	if len(e.env) > 0 {
		localContent = config.InterpolateString(localContent, e.env)
	}

	// Skip the download when we know the file doesn't exist on the target
	// (populated by rdiff's bulk find — avoids one SFTP error per missing config file).
	var remoteData []byte
	if !RemoteFilesKnown(ctx) || RemoteFileExists(ctx, remotePath) {
		var err error
		remoteData, err = e.conn.Download(remotePath)
		if err != nil {
			if cr.Sudo || target.Sudo {
				quoted := "'" + strings.ReplaceAll(remotePath, "'", `'\''`) + "'"
				if stdout, _, code, runErr := e.conn.RunSudo("cat " + quoted); runErr == nil && code == 0 {
					remoteData = []byte(stdout)
				}
			}
		}
	}

	fromLabel := "remote: " + remotePath
	toLabel := "local: " + localPath

	diff, changed := TextDiff(localContent, string(remoteData), fromLabel, toLabel)
	if !changed {
		r.Status = StatusEqual
		r.Elapsed = time.Since(start)
		return r, nil
	}

	r.Diff = diff

	if IsDryRun(ctx) {
		r.Status = StatusChanged
		r.Elapsed = time.Since(start)
		return r, nil
	}

	useSudo := cr.Sudo || target.Sudo
	if err := e.conn.UploadBytes([]byte(localContent), remotePath, 0644, useSudo); err != nil {
		r.Status = StatusFailed
		r.Err = fmt.Errorf("upload %s: %w", localPath, err)
		r.Elapsed = time.Since(start)
		return r, nil
	}

	r.Status = StatusChanged
	r.Size = int64(len(localContent))
	r.Elapsed = time.Since(start)
	if cr.Post.Cmd != "" {
		r.PostActions = []PostAction{{Cmd: cr.Post.Cmd, Sudo: cr.Post.Sudo || target.Sudo}}
	}
	return r, nil
}

// executeMulti handles multi-src config: uploads each file to remoteDest/relPath.
// Glob patterns use tree mode (preserve subdirectory structure); named paths use basename.
// All diffs are aggregated into r.Diff. Status is Changed if any file changed.
func (e *ConfigExecutor) executeMulti(ctx context.Context, cr config.CueRef, target config.Target, r Result, srcs []resolvedSrc, start time.Time) (Result, error) {
	remoteDest := joinRemotePath(e.conn, target.Dir, strings.TrimRight(cr.Dest, "/"))
	sep := "/"
	if e.conn != nil {
		sep = e.conn.PathSep()
	}
	useSudo := cr.Sudo || target.Sudo
	dryRun := IsDryRun(ctx)
	anyChanged := false
	var changedCount int
	var totalSize int64
	var diffBuf strings.Builder
	progressFn := FileProgressFrom(ctx)

	for i, sf := range srcs {
		localPath := sf.path
		rel := remoteRelPath(localPath, sf.pattern)
		// Convert forward slashes in rel to remote path separator.
		remoteRel := strings.ReplaceAll(rel, "/", sep)
		remotePath := remoteDest + sep + remoteRel

		localData, err := os.ReadFile(localPath)
		if err != nil {
			r.Status = StatusFailed
			r.Err = fmt.Errorf("read %s: %w", localPath, err)
			return r, nil
		}

		localContent := string(localData)
		if len(e.env) > 0 {
			localContent = config.InterpolateString(localContent, e.env)
		}

		var remoteData []byte
		if !RemoteFilesKnown(ctx) || RemoteFileExists(ctx, remotePath) {
			remoteData, err = e.conn.Download(remotePath)
			if err != nil && (cr.Sudo || target.Sudo) {
				quoted := "'" + strings.ReplaceAll(remotePath, "'", `'\''`) + "'"
				if stdout, _, code, runErr := e.conn.RunSudo("cat " + quoted); runErr == nil && code == 0 {
					remoteData = []byte(stdout)
				}
			}
		}

		fromLabel := "remote: " + remotePath
		toLabel := "local: " + localPath
		diff, changed := TextDiff(localContent, string(remoteData), fromLabel, toLabel)
		if progressFn != nil {
			progressFn(cr.Name, i+1, len(srcs))
		}
		if !changed {
			continue
		}
		anyChanged = true
		changedCount++
		diffBuf.WriteString(diff)

		if dryRun {
			continue
		}

		// Tree mode: ensure remote parent directory exists before upload.
		if strings.Contains(rel, "/") {
			relDir := rel[:strings.LastIndex(rel, "/")]
			remoteParent := remoteDest + sep + strings.ReplaceAll(relDir, "/", sep)
			mkdirCmd := "mkdir -p " + shellQuote(remoteParent)
			if useSudo {
				e.conn.RunSudo(mkdirCmd)
			} else {
				e.conn.Run(mkdirCmd)
			}
		}

		if err := e.conn.UploadBytes([]byte(localContent), remotePath, 0644, useSudo); err != nil {
			r.Status = StatusFailed
			r.Err = fmt.Errorf("upload %s: %w", localPath, err)
			r.Elapsed = time.Since(start)
			return r, nil
		}
		totalSize += int64(len(localContent))
	}

	if !anyChanged {
		r.Status = StatusEqual
		r.FileTotal = len(srcs)
		r.FileChanged = 0
		r.Elapsed = time.Since(start)
		return r, nil
	}

	r.Status = StatusChanged
	r.Diff = strings.TrimRight(diffBuf.String(), "\n")
	r.Size = totalSize
	r.FileTotal = len(srcs)
	r.FileChanged = changedCount
	r.Elapsed = time.Since(start)
	if !dryRun && cr.Post.Cmd != "" {
		r.PostActions = []PostAction{{Cmd: cr.Post.Cmd, Sudo: cr.Post.Sudo || target.Sudo}}
	}
	return r, nil
}
