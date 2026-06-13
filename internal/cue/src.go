// internal/cue/src.go
package cue

import (
	"fmt"
	"path/filepath"
	"strings"

	"git.disroot.org/jmy/regis/internal/config"
)

// resolvedSrc is a local file path paired with its original pattern.
type resolvedSrc struct {
	path    string // concrete local file path
	pattern string // original pattern (may be a glob or a named path)
}

// expandSrcResolved expands a StringOrList of src paths, resolving any glob patterns.
// Retains the originating pattern for
// each resolved file — used by config tree-mode and pack to preserve relative paths.
func expandSrcResolved(srcs config.StringOrList) ([]resolvedSrc, error) {
	var result []resolvedSrc
	for _, s := range srcs {
		if strings.ContainsAny(s, "*?[") {
			matches, err := filepath.Glob(s)
			if err != nil {
				return nil, fmt.Errorf("glob %q: %w", s, err)
			}
			for _, m := range matches {
				result = append(result, resolvedSrc{path: m, pattern: s})
			}
		} else {
			result = append(result, resolvedSrc{path: s, pattern: s})
		}
	}
	return result, nil
}

// globRoot returns the directory prefix of a glob pattern — everything before
// the first wildcard segment, with a trailing slash.
// e.g. "application/**" → "application/", "*.conf" → "".
func globRoot(pattern string) string {
	parts := strings.Split(filepath.ToSlash(pattern), "/")
	var rootParts []string
	for _, p := range parts {
		if strings.ContainsAny(p, "*?[") {
			break
		}
		rootParts = append(rootParts, p)
	}
	if len(rootParts) == 0 {
		return ""
	}
	return strings.Join(rootParts, "/") + "/"
}

// remoteRelPath computes the relative destination path for a resolved src file.
// Glob patterns → tree mode: strip the glob root and preserve subdirectory structure.
// Named paths   → flat mode: return basename only (backward-compatible).
func remoteRelPath(localPath, pattern string) string {
	if !strings.ContainsAny(pattern, "*?[") {
		return filepath.Base(localPath)
	}
	root := globRoot(pattern)
	rel := filepath.ToSlash(localPath)
	if root != "" {
		rel = strings.TrimPrefix(rel, root)
	}
	return rel
}
