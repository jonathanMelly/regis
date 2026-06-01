// cmd/regis/cmd/debug.go
package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"time"

	"git.disroot.org/jmy/regis/internal/cue"
)

// debugConn wraps any cue.SSHConn and logs every method call to stderr.
// Activated by --debug flag. Never used in production paths.
type debugConn struct {
	inner cue.SSHConn
}

// WrapDebug returns conn wrapped with debug logging, or conn unchanged if debug=false.
func WrapDebug(conn cue.SSHConn, debug bool) cue.SSHConn {
	if !debug || conn == nil {
		return conn
	}
	return &debugConn{inner: conn}
}

func (d *debugConn) Run(cmd string) (string, string, int, error) {
	fmt.Fprintf(os.Stderr, "[debug] run: %s\n", cmd)
	stdout, stderr, code, err := d.inner.Run(cmd)
	debugResult(stdout, stderr, code, err)
	return stdout, stderr, code, err
}

func (d *debugConn) RunSudo(cmd string) (string, string, int, error) {
	fmt.Fprintf(os.Stderr, "[debug] runsudo: %s\n", cmd)
	stdout, stderr, code, err := d.inner.RunSudo(cmd)
	debugResult(stdout, stderr, code, err)
	return stdout, stderr, code, err
}

func (d *debugConn) RunWithEnv(cmd string, env map[string]string) (string, string, int, error) {
	fmt.Fprintf(os.Stderr, "[debug] runenv: %s  env=%v\n", cmd, env)
	stdout, stderr, code, err := d.inner.RunWithEnv(cmd, env)
	debugResult(stdout, stderr, code, err)
	return stdout, stderr, code, err
}

func (d *debugConn) Upload(localPath, remotePath string, mode fs.FileMode, sudo bool) error {
	fmt.Fprintf(os.Stderr, "[debug] upload: %s → %s  mode=%o sudo=%v\n", localPath, remotePath, mode, sudo)
	err := d.inner.Upload(localPath, remotePath, mode, sudo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[debug]   error: %v\n", err)
	}
	return err
}

func (d *debugConn) UploadBytes(data []byte, remotePath string, mode fs.FileMode, sudo bool) error {
	fmt.Fprintf(os.Stderr, "[debug] upload-bytes: → %s  len=%d mode=%o sudo=%v\n", remotePath, len(data), mode, sudo)
	err := d.inner.UploadBytes(data, remotePath, mode, sudo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[debug]   error: %v\n", err)
	}
	return err
}

func (d *debugConn) Download(remotePath string) ([]byte, error) {
	fmt.Fprintf(os.Stderr, "[debug] download: %s\n", remotePath)
	data, err := d.inner.Download(remotePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[debug]   error: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "[debug]   ok: %d bytes\n", len(data))
	}
	return data, err
}

func (d *debugConn) MD5(remotePath string) (string, error) {
	fmt.Fprintf(os.Stderr, "[debug] md5: %s\n", remotePath)
	hash, err := d.inner.MD5(remotePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[debug]   error: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "[debug]   ok: %s\n", hash)
	}
	return hash, err
}

func (d *debugConn) Stat(remotePath string) (time.Time, error) {
	fmt.Fprintf(os.Stderr, "[debug] stat: %s\n", remotePath)
	t, err := d.inner.Stat(remotePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[debug]   error: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "[debug]   ok: %s\n", t)
	}
	return t, err
}

func (d *debugConn) Exists(remotePath string) (bool, error) {
	fmt.Fprintf(os.Stderr, "[debug] exists: %s\n", remotePath)
	ok, err := d.inner.Exists(remotePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[debug]   error: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "[debug]   ok: %v\n", ok)
	}
	return ok, err
}

func (d *debugConn) PathSep() string { return d.inner.PathSep() }

func debugResult(stdout, stderr string, code int, err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "[debug]   error: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "[debug]   exit=%d", code)
	if stdout != "" {
		fmt.Fprintf(os.Stderr, "  stdout=%q", truncateDebug(stdout))
	}
	if stderr != "" {
		fmt.Fprintf(os.Stderr, "  stderr=%q", truncateDebug(stderr))
	}
	fmt.Fprintln(os.Stderr)
}

func truncateDebug(s string) string {
	const max = 120
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
