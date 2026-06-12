// internal/ssh/transfer.go
package ssh

import (
	"crypto/md5"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/pkg/sftp"
)

// UploadBytes writes data to remotePath using SFTP + atomic mv.
// Writes to remotePath+".new" first, then renames (with optional sudo).
func (c *Conn) UploadBytes(data []byte, remotePath string, mode fs.FileMode, useSudo bool) error {
	// Expand ~ up front — shellQuote wraps paths in single quotes which suppresses
	// shell ~ expansion, so both SFTP and shell commands need the literal home path.
	var err error
	remotePath, err = c.ExpandHome(remotePath)
	if err != nil {
		return err
	}
	tmpPath := remotePath + ".new"

	// Ensure parent directory exists (first deploy to a new path).
	if idx := strings.LastIndex(remotePath, "/"); idx > 0 {
		mkdirCmd := "mkdir -p " + shellQuote(remotePath[:idx])
		if useSudo {
			mkdirCmd = "sudo " + mkdirCmd
		}
		c.Run(mkdirCmd) // best-effort; sc.Create surfaces the real error if this also fails
	}

	// Write via SFTP (transplanted from jelastic-gateway)
	sc, err := sftp.NewClient(c.client)
	if err != nil {
		return fmt.Errorf("sftp client: %w", err)
	}
	defer sc.Close()

	f, err := sc.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("sftp create %s: %w", tmpPath, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("sftp write %s: %w", tmpPath, err)
	}
	f.Close()

	// Set permissions
	if _, stderr, code, err := c.Run(fmt.Sprintf("chmod %o %s", mode, shellQuote(tmpPath))); err != nil || code != 0 {
		return fmt.Errorf("chmod %s (exit %d): %s", tmpPath, code, stderr)
	}

	// Atomic rename (with optional sudo for protected paths)
	mvCmd := fmt.Sprintf("mv %s %s", shellQuote(tmpPath), shellQuote(remotePath))
	if useSudo {
		mvCmd = "sudo " + mvCmd
	}
	if _, stderr, code, err := c.Run(mvCmd); err != nil || code != 0 {
		return fmt.Errorf("mv %s → %s (exit %d): %s", tmpPath, remotePath, code, stderr)
	}
	return nil
}

// Upload reads a local file and calls UploadBytes.
func (c *Conn) Upload(localPath, remotePath string, mode fs.FileMode, useSudo bool) error {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", localPath, err)
	}
	if mode == 0 {
		if fi, err := os.Stat(localPath); err == nil {
			mode = fi.Mode()
		} else {
			mode = 0644
		}
	}
	return c.UploadBytes(data, remotePath, mode, useSudo)
}

// Download reads a remote file and returns its bytes via SFTP.
func (c *Conn) Download(remotePath string) ([]byte, error) {
	var err error
	remotePath, err = c.ExpandHome(remotePath)
	if err != nil {
		return nil, err
	}
	sc, err := sftp.NewClient(c.client)
	if err != nil {
		return nil, fmt.Errorf("sftp client: %w", err)
	}
	defer sc.Close()
	f, err := sc.Open(remotePath)
	if err != nil {
		return nil, fmt.Errorf("sftp open %s: %w", remotePath, err)
	}
	defer f.Close()
	return io.ReadAll(f)
}

// Hash returns the hex hash of a remote file using md5sum (Linux) or md5 -q (macOS).
func (c *Conn) Hash(remotePath string) (string, error) {
	var err error
	remotePath, err = c.ExpandHome(remotePath)
	if err != nil {
		return "", err
	}
	stdout, _, code, err := c.Run("md5sum " + shellQuote(remotePath))
	if err != nil || code != 0 {
		var stderr string
		stdout, stderr, code, err = c.Run("md5 -q " + shellQuote(remotePath))
		if err != nil || code != 0 {
			return "", fmt.Errorf("hash %s (exit %d): %s", remotePath, code, stderr)
		}
	}
	parts := strings.Fields(strings.TrimSpace(stdout))
	if len(parts) == 0 {
		return "", fmt.Errorf("unexpected hash output: %q", stdout)
	}
	return parts[0], nil
}

// LocalHash computes the hex hash of a local file.
func LocalHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// Exists reports whether remotePath exists on the target.
func (c *Conn) Exists(remotePath string) (bool, error) {
	var err error
	remotePath, err = c.ExpandHome(remotePath)
	if err != nil {
		return false, err
	}
	_, _, code, err := c.Run("test -e " + shellQuote(remotePath))
	if err != nil {
		return false, err
	}
	return code == 0, nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
