// cmd/regis/cmd/helpers.go — shared helpers for regis subcommands.
package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"git.disroot.org/jmy/regis/internal/config"
	regssh "git.disroot.org/jmy/regis/internal/ssh"
)

// withConn loads config, resolves target, dials SSH, and calls fn.
func withConn(gf *GlobalFlags, fn func(*regssh.Conn, config.Target, *config.Config) error) error {
	cfg, err := config.Load(gf.File)
	if err != nil {
		return err
	}
	var tgtNames []string
	for _, t := range cfg.Targets {
		tgtNames = append(tgtNames, t.Name)
	}
	selected := SelectTargets(tgtNames, gf.Target)
	if len(selected) == 0 {
		return fmt.Errorf("no targets matched")
	}
	var tgt config.Target
	for i := range cfg.Targets {
		if cfg.Targets[i].Name == selected[0] {
			if err := config.InterpolateForTarget(cfg, &cfg.Targets[i]); err != nil {
				return err
			}
			tgt = cfg.Targets[i]
			break
		}
	}
	if gf.Debug {
		port := "22"
		if tgt.Port != "" {
			port = tgt.Port
		}
		fmt.Fprintf(os.Stderr, "[debug] dialing %s@%s:%s\n", tgt.User, tgt.Host, port)
	}
	conn, err := regssh.Dial(tgt)
	if gf.Debug && err != nil {
		fmt.Fprintf(os.Stderr, "[debug] dial error: %v\n", err)
	}
	if err != nil {
		return err
	}
	defer conn.Close()

	expanded, expandErr := conn.ExpandHome(tgt.Dir)
	if expandErr != nil {
		return fmt.Errorf("expand home: %w", expandErr)
	}
	tgt.Dir = expanded

	return fn(conn, tgt, cfg)
}

// stateConn is the SSH subset needed by state helper functions.
type stateConn interface {
	Run(cmd string) (stdout, stderr string, exitCode int, err error)
	Download(remotePath string) ([]byte, error)
	Upload(localPath, remotePath string, mode fs.FileMode, useSudo bool) error
	UploadBytes(data []byte, remotePath string, mode fs.FileMode, useSudo bool) error
}

// effectiveStateDir returns the local state directory, defaulting to .regis-states.
func effectiveStateDir(cfg *config.Config) string {
	if cfg.State.LocalDir != "" {
		return cfg.State.LocalDir
	}
	return ".regis-states"
}

// shortHash returns the first 8 characters of a hash string.
func shortHash(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}

// hashesEqual reports whether two hash maps are identical.
func hashesEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// resolveStateDir returns cfg.State.Dir when set, or falls back to
// <tgt.Dir>/.regis-states — the default state archive location.
func resolveStateDir(cfg *config.Config, tgt config.Target) string {
	if cfg.State.Dir != "" {
		return cfg.State.Dir
	}
	return path.Join(tgt.Dir, ".regis-states")
}

// latestLocalStateFile returns the path to the most recent state YAML file in
// localDir/<target>/ (highest lexicographic name). Returns "" when none exist.
func latestLocalStateFile(localDir string) string {
	entries, err := os.ReadDir(localDir)
	if err != nil {
		return ""
	}
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if !e.IsDir() {
			continue
		}
		subEntries, err := os.ReadDir(filepath.Join(localDir, e.Name()))
		if err != nil {
			continue
		}
		for j := len(subEntries) - 1; j >= 0; j-- {
			sub := subEntries[j]
			if !sub.IsDir() && filepath.Ext(sub.Name()) == ".yml" {
				candidate := filepath.Join(localDir, e.Name(), sub.Name())
				if _, statErr := os.Stat(candidate); statErr == nil {
					return candidate
				}
			}
		}
	}
	return ""
}
