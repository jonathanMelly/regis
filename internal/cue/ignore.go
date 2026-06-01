// internal/cue/ignore.go
package cue

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// loadIgnorePatterns reads .regisignore from dir and returns the denylist patterns.
// Lines starting with # and empty lines are ignored. Missing file is not an error.
func loadIgnorePatterns(dir string) ([]string, error) {
	f, err := os.Open(filepath.Join(dir, ".regisignore"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var patterns []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns, sc.Err()
}

// applyIgnore filters resolved src files by removing any whose forward-slash
// path matches one of the denylist patterns (filepath.Match semantics).
func applyIgnore(srcs []resolvedSrc, patterns []string) []resolvedSrc {
	if len(patterns) == 0 {
		return srcs
	}
	var out []resolvedSrc
	for _, s := range srcs {
		fwdPath := filepath.ToSlash(s.path)
		ignored := false
		for _, pat := range patterns {
			if matched, _ := filepath.Match(pat, fwdPath); matched {
				ignored = true
				break
			}
			// Also match against just the basename for simple patterns like "*.log"
			if matched, _ := filepath.Match(pat, filepath.Base(s.path)); matched {
				ignored = true
				break
			}
		}
		if !ignored {
			out = append(out, s)
		}
	}
	return out
}
