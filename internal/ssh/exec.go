// internal/ssh/exec.go
package ssh

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"

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

// RunStream executes cmd and calls onLine for each line of output as it arrives.
// Returns accumulated stdout/stderr identical to Run.
func (c *Conn) RunStream(cmd string, onLine func(line string, isStderr bool)) (stdout, stderr string, exitCode int, err error) {
	sess, err := c.client.NewSession()
	if err != nil {
		return "", "", -1, fmt.Errorf("new session: %w", err)
	}
	defer sess.Close()

	outR, outW := io.Pipe()
	errR, errW := io.Pipe()
	sess.Stdout = outW
	sess.Stderr = errW

	var outBuf, errBuf strings.Builder
	var wg sync.WaitGroup

	scanLines := func(r io.Reader, isStderr bool, buf *strings.Builder) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			line := sc.Text()
			buf.WriteString(line + "\n")
			onLine(line, isStderr)
		}
	}

	wg.Add(2)
	go scanLines(outR, false, &outBuf)
	go scanLines(errR, true, &errBuf)

	runErr := sess.Run(cmd)
	outW.Close()
	errW.Close()
	wg.Wait()

	if runErr != nil {
		if exitErr, ok := runErr.(*gossh.ExitError); ok {
			return outBuf.String(), errBuf.String(), exitErr.ExitStatus(), nil
		}
		return outBuf.String(), errBuf.String(), -1, runErr
	}
	return outBuf.String(), errBuf.String(), 0, nil
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
