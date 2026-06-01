// internal/cue/diff.go
package cue

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/pmezard/go-difflib/difflib"

	sshpkg "git.disroot.org/jmy/regis/internal/ssh"
)

// TextDiff computes a unified diff between local and remote text content.
// fromFile and toFile label the --- and +++ header lines (e.g. remote path and local path).
// Returns the diff string and whether the content differs.
func TextDiff(local, remote, fromFile, toFile string) (diff string, changed bool) {
	if local == remote {
		return "", false
	}
	ud := difflib.UnifiedDiff{
		A:        difflib.SplitLines(remote),
		B:        difflib.SplitLines(local),
		FromFile: fromFile,
		ToFile:   toFile,
		Context:  3,
	}
	text, _ := difflib.GetUnifiedDiffString(ud)
	return text, true
}

// SecretDiff computes a diff of .env-format files with values masked.
// Keys are shown; values are replaced with ***.
func SecretDiff(local, remote string, preserve []string) (diff string, changed bool) {
	localKeys := parseEnvKeys(local)
	remoteKeys := parseEnvKeys(remote)

	preserveSet := make(map[string]bool)
	for _, k := range preserve {
		preserveSet[k] = true
	}

	var sb strings.Builder
	allKeys := mergeKeys(localKeys, remoteKeys)
	anyChanged := false

	for _, key := range allKeys {
		lv, lOk := localKeys[key]
		rv, rOk := remoteKeys[key]
		_ = lv
		_ = rv
		if preserveSet[key] {
			continue // preserve keys never shown as changed
		}
		switch {
		case lOk && !rOk:
			fmt.Fprintf(&sb, "+ %s=***\n", key)
			anyChanged = true
		case !lOk && rOk:
			fmt.Fprintf(&sb, "- %s=***\n", key)
			anyChanged = true
		case lOk && rOk && localKeys[key] != remoteKeys[key]:
			fmt.Fprintf(&sb, "~ %s=*** (changed)\n", key)
			anyChanged = true
		}
	}
	return sb.String(), anyChanged
}

// MergeSecrets merges local .env content into remote, preserving listed keys.
// Returns the diff string and the merged .env content to upload.
func MergeSecrets(local, remote string, preserve []string) (diff string, merged string) {
	localMap := parseEnvKeys(local)
	remoteMap := parseEnvKeys(remote)

	preserveSet := make(map[string]bool)
	for _, k := range preserve {
		preserveSet[k] = true
	}

	result := make(map[string]string)
	// Start with local values
	for k, v := range localMap {
		result[k] = v
	}
	// Overwrite preserve keys from remote
	for k, v := range remoteMap {
		if preserveSet[k] {
			result[k] = v
		}
	}

	var sb strings.Builder
	for k, v := range result {
		fmt.Fprintf(&sb, "%s=%s\n", k, v)
	}
	merged = sb.String()

	d, _ := SecretDiff(local, remote, preserve)
	return d, merged
}

func parseEnvKeys(content string) map[string]string {
	m := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		m[line[:idx]] = line[idx+1:]
	}
	return m
}

func mergeKeys(a, b map[string]string) []string {
	seen := make(map[string]bool)
	var keys []string
	for k := range a {
		if !seen[k] {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	for k := range b {
		if !seen[k] {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	return keys
}

// LocalMD5 is a thin re-export of ssh.LocalMD5 for use by cue executors.
func LocalMD5(path string) (string, error) {
	return sshpkg.LocalMD5(path)
}
