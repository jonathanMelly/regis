// internal/ssh/exec.go
package ssh

import (
	"bytes"
	"fmt"
	"strings"

	gossh "golang.org/x/crypto/ssh"
)

// Run executes cmd on the remote host. A non-zero exit code is NOT an error —
// check the returned exitCode. Returns connection errors as err.
func (c *Conn) Run(cmd string) (stdout, stderr string, exitCode int, err error) {
	sess, err := c.client.NewSession()
	if err != nil {
		return "", "", -1, fmt.Errorf("new session: %w", err)
	}
	defer sess.Close()

	var outBuf, errBuf bytes.Buffer
	sess.Stdout = &outBuf
	sess.Stderr = &errBuf

	runErr := sess.Run(cmd)
	stdout = outBuf.String()
	stderr = errBuf.String()

	if runErr != nil {
		if exitErr, ok := runErr.(*gossh.ExitError); ok {
			return stdout, stderr, exitErr.ExitStatus(), nil
		}
		return stdout, stderr, -1, runErr
	}
	return stdout, stderr, 0, nil
}

// RunSudo prepends "sudo " to cmd.
func (c *Conn) RunSudo(cmd string) (stdout, stderr string, exitCode int, err error) {
	return c.Run("sudo " + cmd)
}

// RunWithEnv exports env vars before executing cmd.
func (c *Conn) RunWithEnv(cmd string, env map[string]string) (stdout, stderr string, exitCode int, err error) {
	if len(env) == 0 {
		return c.Run(cmd)
	}
	var sb strings.Builder
	for k, v := range env {
		fmt.Fprintf(&sb, "export %s=%q; ", k, v)
	}
	sb.WriteString(cmd)
	return c.Run(sb.String())
}
