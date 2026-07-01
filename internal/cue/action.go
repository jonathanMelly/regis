// internal/cue/action.go
// doc:nature action
// Runs a shell command. local: true runs on your machine; default runs on SSH target.
// Always executes (no change detection).
// compensation: "cmd" or {shell, sudo} — runs a compensation command when on_error: compensate triggers.
package cue

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"git.disroot.org/jmy/regis/internal/config"
)

// ActionExecutor handles nature: action cues (local or remote shell commands).
type ActionExecutor struct{ conn SSHConn }

// NewActionExecutor creates an ActionExecutor.
func NewActionExecutor(conn SSHConn) *ActionExecutor { return &ActionExecutor{conn: conn} }

// Execute runs the cue's shell command (locally or remotely) and evaluates when-expressions.
func (e *ActionExecutor) Execute(ctx context.Context, _ SSHConn, cr config.CueRef, target config.Target) (Result, error) {
	start := time.Now()
	r := Result{
		CueName:        cr.Name,
		Nature:         "action",
		IsLocal:        cr.Local,
		AffectsState: cr.AffectsState,
		Cmd:            cr.Shell,
	}

	// Check-only (rdiff): skip execution — action outcome cannot be predicted without running
	if !cr.Local && IsCheckOnly(ctx) {
		r.Status = StatusSkipped
		r.Elapsed = time.Since(start)
		return r, nil
	}

	var stdout, stderr string
	var exitCode int
	var runErr error

	remoteShell := cr.Shell
	if !cr.Local && target.Dir != "" {
		remoteShell = "cd " + shellQuote(target.Dir) + " && " + cr.Shell
	}

	useSudo := !cr.Local && (cr.Sudo || target.Sudo)
	onLine := OutputLineFrom(ctx)
	if cr.Local {
		if onLine != nil {
			stdout, stderr, exitCode, runErr = runLocalStream(ctx, cr.Shell, cr.Env, onLine)
		} else {
			stdout, stderr, exitCode, runErr = runLocal(ctx, cr.Shell, cr.Env)
		}
	} else if useSudo {
		cmd := remoteShell
		if len(cr.Env) > 0 {
			var sb strings.Builder
			for k, v := range cr.Env {
				fmt.Fprintf(&sb, "export %s=%q; ", k, v)
			}
			sb.WriteString(cmd)
			cmd = sb.String()
		}
		if onLine != nil {
			stdout, stderr, exitCode, runErr = e.conn.RunStream("sudo "+cmd, onLine)
		} else {
			stdout, stderr, exitCode, runErr = e.conn.RunSudo(cmd)
		}
	} else {
		if onLine != nil {
			// Build env prefix same as RunWithEnv.
			fullCmd := remoteShell
			if len(cr.Env) > 0 {
				var sb strings.Builder
				for k, v := range cr.Env {
					fmt.Fprintf(&sb, "export %s=%q; ", k, v)
				}
				sb.WriteString(fullCmd)
				fullCmd = sb.String()
			}
			stdout, stderr, exitCode, runErr = e.conn.RunStream(fullCmd, onLine)
		} else {
			stdout, stderr, exitCode, runErr = e.conn.RunWithEnv(remoteShell, cr.Env)
		}
	}

	if runErr != nil {
		r.Status = StatusFailed
		r.Err = runErr
		r.Stdout = stdout
		r.Stderr = stderr
		r.Elapsed = time.Since(start)
		return r, nil
	}

	r.Stdout = stdout
	r.Stderr = stderr

	// Evaluate failed_when
	failed, err := evalWhen(ctx, cr.FailedWhen, stdout, stderr, exitCode)
	if err != nil {
		r.Status = StatusFailed
		r.Err = fmt.Errorf("failed_when eval: %w", err)
		r.Elapsed = time.Since(start)
		return r, nil
	}
	if failed {
		r.Status = StatusFailed
		r.Err = fmt.Errorf("exit %d: %s", exitCode, strings.TrimSpace(stderr))
		r.Elapsed = time.Since(start)
		return r, nil
	}

	// Default: non-zero exit = failed (when no failed_when expression)
	if exitCode != 0 && cr.FailedWhen == (config.WhenExpr{}) {
		r.Status = StatusFailed
		r.Err = fmt.Errorf("exit %d: %s", exitCode, strings.TrimSpace(stderr))
		r.Elapsed = time.Since(start)
		return r, nil
	}

	// Evaluate changed_when
	changed, err := evalWhen(ctx, cr.ChangedWhen, stdout, stderr, exitCode)
	if err != nil {
		r.Status = StatusFailed
		r.Err = fmt.Errorf("changed_when eval: %w", err)
		r.Elapsed = time.Since(start)
		return r, nil
	}

	// Default: action always counts as changed unless changed_when says otherwise
	if cr.ChangedWhen == (config.WhenExpr{}) {
		changed = true
	}

	if changed {
		r.Status = StatusChanged
		if cr.Post.Cmd != "" && !cr.Local {
			r.PostActions = []PostAction{{Cmd: cr.Post.Cmd, Sudo: cr.Post.Sudo || target.Sudo}}
		}
	} else {
		r.Status = StatusEqual
	}
	r.Elapsed = time.Since(start)
	return r, nil
}

func evalWhen(ctx context.Context, w config.WhenExpr, stdout, stderr string, exitCode int) (bool, error) {
	switch {
	case w.BoolLiteral != nil:
		return *w.BoolLiteral, nil
	case w.Expression != "":
		return EvalWhenExpr(w.Expression, stdout, stderr, exitCode)
	case w.Shell != "":
		_, _, code, err := runLocal(ctx, w.Shell, nil)
		if err != nil {
			return false, err
		}
		switch code {
		case 0:
			return true, nil
		case 1:
			return false, nil
		default:
			return false, fmt.Errorf("shell probe exited %d (2+ = error)", code)
		}
	}
	return false, nil // zero WhenExpr = not set
}

func runLocal(ctx context.Context, shell string, env map[string]string) (stdout, stderr string, exitCode int, err error) {
	var args []string
	if os.PathSeparator == '\\' {
		// Windows: expand $VAR references from env map so cmd.exe sees literal paths.
		// cmd.exe is always present and its > redirect writes raw bytes (no encoding change).
		args = []string{"cmd", "/C", expandEnvRefs(shell, env)}
	} else {
		args = []string{"sh", "-c", shell}
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	if dir := LocalDirFrom(ctx); dir != "" {
		cmd.Dir = dir
	}
	if len(env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	if runErr := cmd.Run(); runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			return outBuf.String(), errBuf.String(), exitErr.ExitCode(), nil
		}
		return outBuf.String(), errBuf.String(), -1, runErr
	}
	return outBuf.String(), errBuf.String(), 0, nil
}

// runLocalStream runs shell with streaming output via onLine.
func runLocalStream(ctx context.Context, shell string, env map[string]string, onLine func(string, bool)) (stdout, stderr string, exitCode int, err error) {
	var args []string
	if os.PathSeparator == '\\' {
		args = []string{"cmd", "/C", expandEnvRefs(shell, env)}
	} else {
		args = []string{"sh", "-c", shell}
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	if dir := LocalDirFrom(ctx); dir != "" {
		cmd.Dir = dir
	}
	if len(env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}

	outR, outW := io.Pipe()
	errR, errW := io.Pipe()
	cmd.Stdout = outW
	cmd.Stderr = errW

	var outBuf, errBuf strings.Builder
	var wg sync.WaitGroup

	scan := func(r io.Reader, isStderr bool, buf *strings.Builder) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			line := sc.Text()
			buf.WriteString(line + "\n")
			onLine(line, isStderr)
		}
	}

	wg.Add(2)
	go scan(outR, false, &outBuf)
	go scan(errR, true, &errBuf)

	if runErr := cmd.Start(); runErr != nil {
		outW.Close()
		errW.Close()
		wg.Wait()
		return "", "", -1, runErr
	}
	runErr := cmd.Wait()
	outW.Close()
	errW.Close()
	wg.Wait()

	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			return outBuf.String(), errBuf.String(), exitErr.ExitCode(), nil
		}
		return outBuf.String(), errBuf.String(), -1, runErr
	}
	return outBuf.String(), errBuf.String(), 0, nil
}

// expandEnvRefs replaces $KEY references in s with values from env.
// Used on Windows so cmd.exe receives literal values rather than unexpanded $VAR syntax.
func expandEnvRefs(s string, env map[string]string) string {
	for k, v := range env {
		s = strings.ReplaceAll(s, "$"+k, v)
	}
	return s
}
