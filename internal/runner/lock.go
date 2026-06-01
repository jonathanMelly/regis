// internal/runner/lock.go
package runner

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"git.disroot.org/jmy/regis/internal/config"
)

// errSkippedLocked is returned when on_locked: skip and the target is locked.
var errSkippedLocked = errors.New("target is locked")

// lockConn is the subset of an SSH connection needed for locking.
type lockConn interface {
	Run(cmd string) (string, string, int, error)
}

// targetLockPath returns the remote lock path for a target.
// Uses a directory — mkdir is atomic on Linux/ext4/NFS, providing a reliable mutex.
func targetLockPath(target config.Target) string {
	return path.Join(target.Dir, ".regis.lock")
}

// acquireLock tries to create the lock directory on the target.
// Behaviour on contention is controlled by cfg.OnLocked and cfg.LockWait.
//   - skip: returns errSkippedLocked immediately — caller skips the deploy gracefully.
//   - fail: returns an error — caller aborts.
//   - wait (default): polls every 2s until the lock is acquired or LockWait expires.
func acquireLock(conn lockConn, lp string, cfg config.ConcurrencyConfig) error {
	tryOnce := func() bool {
		_, _, code, _ := conn.Run("mkdir " + singleQuote(lp) + " 2>/dev/null")
		return code == 0
	}

	if tryOnce() {
		return nil
	}

	switch cfg.OnLocked {
	case "skip":
		return errSkippedLocked
	case "fail":
		return fmt.Errorf("target is locked (%s) and on_locked: fail", lp)
	default: // "wait"
		timeout := 30 * time.Second
		if cfg.LockWait != "" {
			if d, err := time.ParseDuration(cfg.LockWait); err == nil {
				timeout = d
			}
		}
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			time.Sleep(2 * time.Second)
			if tryOnce() {
				return nil
			}
		}
		return fmt.Errorf("timeout waiting for lock (%s) after %s", lp, timeout)
	}
}

// releaseLock removes the lock directory. Best-effort — never returns an error.
func releaseLock(conn lockConn, lp string) {
	conn.Run("rmdir " + singleQuote(lp) + " 2>/dev/null; true")
}

// singleQuote wraps s in single quotes, escaping any embedded single quotes.
func singleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
