// internal/cue/prefetch.go
// Free functions for remote file stat/hash operations.
// All use SSHConn.Run() — no new interface methods, no import cycle.
package cue

import (
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

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

// HashRemote returns the hash of a remote file.
// Tries md5sum (Linux) first, falls back to md5 -q (macOS).
func HashRemote(conn SSHConn, remotePath string) (string, error) {
	stdout, _, code, err := conn.Run("md5sum " + shellQuotePrefetch(remotePath))
	if err != nil || code != 0 {
		var stderr string
		stdout, stderr, code, err = conn.Run("md5 -q " + shellQuotePrefetch(remotePath))
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

// StatRemote returns the mtime and size of a remote file in a single stat call.
// Returns zero time and -1 on any error (graceful: Windows, stat unavailable).
func StatRemote(conn SSHConn, remotePath string) (time.Time, int64) {
	stdout, _, code, err := conn.Run("stat -c '%Y %s' " + shellQuotePrefetch(remotePath))
	if err != nil || code != 0 {
		stdout, _, code, err = conn.Run("stat -f '%m %z' " + shellQuotePrefetch(remotePath))
		if err != nil || code != 0 {
			return time.Time{}, -1
		}
	}
	return parseStatLine(strings.TrimSpace(stdout))
}

// bulkBatchSize is the maximum number of paths per SSH stat/hash command.
// SSH channels typically cap payload at ~32 KB; 100 paths with 50-char names ≈ 5 KB.
const bulkBatchSize = 100

// BulkStatRemote fetches mtime and size for multiple remote paths.
// Paths are batched so each SSH call stays well under the channel payload limit.
// Returns a map of remotePath → RemoteStat. Missing paths are absent from the map.
func BulkStatRemote(conn SSHConn, paths []string) map[string]RemoteStat {
	if len(paths) == 0 {
		return nil
	}
	result := make(map[string]RemoteStat, len(paths))
	for i := 0; i < len(paths); i += bulkBatchSize {
		batch := paths[i:min(i+bulkBatchSize, len(paths))]
		args := joinQuoted(batch)
		stdout, _, _, err := conn.Run("stat -c '%Y %s %n' " + args + " 2>/dev/null")
		if err != nil {
			fmt.Fprintf(os.Stderr, "warn: remote stat failed: %v\n", err)
			continue
		}
		if strings.TrimSpace(stdout) == "" {
			stdout, _, _, err = conn.Run("stat -f '%m %z %N' " + args + " 2>/dev/null")
			if err != nil {
				fmt.Fprintf(os.Stderr, "warn: remote stat failed: %v\n", err)
				continue
			}
		}
		for _, line := range strings.Split(stdout, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			// format: "EPOCH SIZE PATHNAME" — split on first two spaces
			parts := strings.SplitN(line, " ", 3)
			if len(parts) < 3 {
				continue
			}
			mtime, size := parseStatLine(parts[0] + " " + parts[1])
			if mtime.IsZero() {
				continue
			}
			result[parts[2]] = RemoteStat{Mtime: mtime, Size: size}
		}
	}
	return result
}

// BulkHashRemote fetches MD5 hashes for multiple remote paths.
// Paths are batched so each SSH call stays well under the channel payload limit.
// Returns a map of remotePath → hash. Missing or errored paths are absent.
func BulkHashRemote(conn SSHConn, paths []string) map[string]string {
	if len(paths) == 0 {
		return nil
	}
	result := make(map[string]string, len(paths))
	for i := 0; i < len(paths); i += bulkBatchSize {
		batch := paths[i:min(i+bulkBatchSize, len(paths))]
		args := joinQuoted(batch)
		stdout, _, _, err := conn.Run("md5sum " + args + " 2>/dev/null")
		if err != nil {
			fmt.Fprintf(os.Stderr, "warn: remote md5sum failed: %v\n", err)
			continue
		}
		for _, line := range strings.Split(stdout, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			// md5sum format: "HASH  PATH" or "HASH PATH"
			idx := strings.IndexByte(line, ' ')
			if idx < 0 {
				continue
			}
			hash := line[:idx]
			path := strings.TrimLeft(line[idx:], " ")
			if path == "" || hash == "" {
				continue
			}
			result[path] = hash
		}
	}
	return result
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// SetRemoteMtime sets the mtime of remotePath to t using touch -d @epoch (GNU/Linux).
// Best-effort: called after binary uploads to enable fast mtime+size comparison on next run.
func SetRemoteMtime(conn SSHConn, remotePath string, t time.Time) {
	epoch := strconv.FormatInt(t.Unix(), 10)
	conn.Run(fmt.Sprintf("touch -d @%s %s", epoch, shellQuotePrefetch(remotePath))) //nolint
}

// parseStatLine parses "EPOCH SIZE" into (mtime, size). Returns zero/−1 on any error.
func parseStatLine(s string) (time.Time, int64) {
	parts := strings.Fields(s)
	if len(parts) < 2 {
		return time.Time{}, -1
	}
	epoch, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, -1
	}
	size, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return time.Time{}, -1
	}
	return time.Unix(epoch, 0), size
}

// joinQuoted returns a space-separated string of single-quoted paths.
func joinQuoted(paths []string) string {
	quoted := make([]string, len(paths))
	for i, p := range paths {
		quoted[i] = shellQuotePrefetch(p)
	}
	return strings.Join(quoted, " ")
}

func shellQuotePrefetch(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
