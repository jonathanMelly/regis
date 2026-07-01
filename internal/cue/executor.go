// internal/cue/executor.go
package cue

import (
	"context"
	"io/fs"

	"git.disroot.org/jmy/regis/internal/config"
)

// SSHConn is the interface executors use to interact with a remote host.
// *ssh.Conn satisfies this in production; mockConn satisfies it in tests.
// Hash/stat operations are free functions in prefetch.go that call Run().
type SSHConn interface {
	Run(cmd string) (stdout, stderr string, exitCode int, err error)
	RunSudo(cmd string) (stdout, stderr string, exitCode int, err error)
	RunWithEnv(cmd string, env map[string]string) (stdout, stderr string, exitCode int, err error)
	RunStream(cmd string, onLine func(line string, isStderr bool)) (stdout, stderr string, exitCode int, err error)
	Upload(localPath, remotePath string, mode fs.FileMode, useSudo bool) error
	UploadBytes(data []byte, remotePath string, mode fs.FileMode, useSudo bool) error
	Download(remotePath string) ([]byte, error)
	Exists(remotePath string) (bool, error)
	// PathSep returns the remote path separator — "/" on Unix, `\` on Windows.
	// Detected once after Dial and cached for the session.
	PathSep() string
}

// Executor executes a single resolved cue and returns its result.
type Executor interface {
	Execute(ctx context.Context, conn SSHConn, cr config.CueRef, target config.Target) (Result, error)
}
