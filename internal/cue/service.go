// internal/cue/service.go
// doc:nature service
// Manages a service on the target. Two checks determine sync state:
//
//  1. Service file diff (when service_file: is set): compares the local unit file against
//     the remote path. For systemd: /etc/systemd/system/<name>.service.
//
//  2. Installed/enabled check:
//     - systemd: runs "systemctl is-enabled <name>" — changed if exit code != 0.
//     - crontab: greps "crontab -l" for the binary path — changed if entry absent.
//     - custom manager: assumed enabled (no remote check; relies on commands: for management).
//
// StatusChanged when either check shows a difference.
// On real run: uploads the service file if changed, then queues a deploy:<name> post-action
// (systemctl daemon-reload + enable for systemd; crontab entry install for crontab).
// rollback: "cmd" or {shell, sudo} — runs a compensation command when on_error: rollback triggers.
package cue

import (
	"context"
	"fmt"
	"os"
	"time"

	"git.disroot.org/jmy/regis/internal/config"
)

// ServiceExecutor handles nature: service cues.
type ServiceExecutor struct {
	conn SSHConn
	env  map[string]string
}

// NewServiceExecutor creates a ServiceExecutor.
// Pass the target's env map (from config.BuildEnvForTarget) to enable ${VAR} rendering
// of service file content before comparison and upload.
func NewServiceExecutor(conn SSHConn, env ...map[string]string) *ServiceExecutor {
	e := &ServiceExecutor{conn: conn}
	if len(env) > 0 {
		e.env = env[0]
	}
	return e
}

// Execute checks service file diff and enabled state, uploads if needed, queues deploy post-action.
func (e *ServiceExecutor) Execute(ctx context.Context, _ SSHConn, cr config.CueRef, target config.Target) (Result, error) {
	start := time.Now()
	r := Result{CueName: cr.Name, Nature: "service", AffectsRelease: false}

	// Check 1: service unit file diff (systemd, when service_file is set)
	fileChanged, diff, rendered, err := e.checkServiceFile(cr, target)
	if err != nil {
		r.Status = StatusFailed
		r.Err = err
		return r, nil
	}

	// Check 2: service installed/enabled on target
	enabled := e.checkEnabled(cr, target)

	if !fileChanged && enabled {
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

	// Upload service file if it changed (upload rendered content with __REMOTE_DIR__ expanded)
	if fileChanged {
		remotePath := fmt.Sprintf("/etc/systemd/system/%s.service", cr.Name)
		useSudo := cr.Sudo || target.Sudo
		if err := e.conn.UploadBytes(rendered, remotePath, 0644, useSudo); err != nil {
			r.Status = StatusFailed
			r.Err = fmt.Errorf("upload %s: %w", cr.ServiceFile, err)
			r.Elapsed = time.Since(start)
			return r, nil
		}
		r.Size = int64(len(rendered))
	}

	// Queue deploy:<name> post-action (daemon-reload+enable for systemd; crontab install for crontab)
	r.PostActions = []PostAction{{Cmd: "deploy:" + cr.Name, Sudo: cr.Sudo || target.Sudo}}
	r.Status = StatusChanged
	r.Elapsed = time.Since(start)
	return r, nil
}

// checkServiceFile compares the local service_file against the remote unit path.
// __REMOTE_DIR__ in the local file is expanded to target.Dir before comparison and upload.
// Returns (false, "", nil, nil) when service_file is not set.
func (e *ServiceExecutor) checkServiceFile(cr config.CueRef, tgt config.Target) (changed bool, diff string, rendered []byte, err error) {
	if cr.ServiceFile == "" {
		return false, "", nil, nil
	}
	localData, err := os.ReadFile(cr.ServiceFile)
	if err != nil {
		return false, "", nil, fmt.Errorf("read %s: %w", cr.ServiceFile, err)
	}
	expanded := config.InterpolateString(string(localData), e.env)
	remotePath := fmt.Sprintf("/etc/systemd/system/%s.service", cr.Name)
	remoteData, _ := e.conn.Download(remotePath)
	diffStr, isChanged := TextDiff(expanded, string(remoteData),
		"remote: "+remotePath,
		"local: "+cr.ServiceFile,
	)
	return isChanged, diffStr, []byte(expanded), nil
}

// checkEnabled returns true when the service is installed/enabled on the target.
// Custom managers always return true (no standard status command available).
func (e *ServiceExecutor) checkEnabled(cr config.CueRef, tgt config.Target) bool {
	switch cr.Manager {
	case "systemd":
		_, _, code, err := e.conn.Run(fmt.Sprintf("systemctl is-enabled %s 2>/dev/null", cr.Name))
		return err == nil && code == 0

	case "crontab":
		binary := cr.Binary
		if binary == "" {
			binary = cr.Name
		}
		var pattern string
		if tgt.Dir != "" {
			pattern = tgt.Dir + "/" + binary
		} else {
			pattern = binary
		}
		_, _, code, err := e.conn.Run(fmt.Sprintf("crontab -l 2>/dev/null | grep -qF %q", pattern))
		return err == nil && code == 0

	default:
		// Custom manager: can't check state without a known status command.
		return true
	}
}
