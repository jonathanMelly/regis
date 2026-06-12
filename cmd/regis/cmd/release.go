// cmd/regis/cmd/release.go
package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/runner"
	regssh "git.disroot.org/jmy/regis/internal/ssh"
	"github.com/spf13/cobra"
)

func newReleaseCommand(gf *GlobalFlags) *cobra.Command {
	rel := &cobra.Command{
		Use:     "release",
		Aliases: []string{"releases"},
		Short:   "manage staged releases on the target",
	}

	rel.AddCommand(&cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "list release snapshots on the target",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConn(gf, func(conn *regssh.Conn, tgt config.Target, cfg *config.Config) error {
				if cfg.Release.Dir == "" {
					return fmt.Errorf("release.dir not configured")
				}
				stdout, _, _, err := conn.Run(fmt.Sprintf(
					"cd %s && ls -dt v* 2>/dev/null || true", cfg.Release.Dir,
				))
				if err != nil {
					return err
				}
				lines := strings.TrimSpace(stdout)
				if lines == "" {
					fmt.Println("no releases yet — run 'regis run' to create one")
					return nil
				}
				fmt.Println(lines)
				return nil
			})
		},
	})

	rel.AddCommand(&cobra.Command{
		Use:   "current",
		Short: "show the release currently deployed on the target",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConn(gf, func(conn *regssh.Conn, tgt config.Target, cfg *config.Config) error {
				data, err := conn.Download(tgt.Dir + "/.regis-release")
				if err != nil {
					fmt.Println("no release manifest found — target may not have been deployed with regis")
					return nil
				}
				var m runner.ReleaseManifest
				if err := yaml.Unmarshal(data, &m); err != nil {
					return fmt.Errorf("parse manifest: %w", err)
				}
				fmt.Printf("release:     %s\n", m.Release)
				fmt.Printf("deployed_at: %s\n", m.DeployedAt.Format("2006-01-02 15:04:05 UTC"))
				fmt.Printf("deployed_by: %s\n", m.DeployedBy)
				fmt.Printf("scenarios:   %s\n", strings.Join(m.Scenarios, ", "))
				return nil
			})
		},
	})

	// rollback — auto-selects the previous release; --release <id> or positional arg for explicit choice.
	rel.AddCommand(func() *cobra.Command {
		var releaseFlag string
		c := &cobra.Command{
			Use:   "rollback [id]",
			Short: "rollback to a previous release (auto-selects previous if no id given)",
			Args:  cobra.MaximumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return withConn(gf, func(conn *regssh.Conn, tgt config.Target, cfg *config.Config) error {
					relDir := cfg.Release.Dir
					if relDir == "" {
						return fmt.Errorf("release.dir not configured")
					}
					localDir := effectiveLocalDir(cfg)

					var releaseID string
					switch {
					case len(args) == 1:
						releaseID = args[0]
					case releaseFlag != "":
						releaseID = releaseFlag
					default:
						stdout, _, _, err := conn.Run(fmt.Sprintf(
							"cd %s && ls -dt v* 2>/dev/null || true", relDir,
						))
						if err != nil {
							return fmt.Errorf("list releases: %w", err)
						}
						releases := strings.Fields(strings.TrimSpace(stdout))
						if len(releases) < 2 {
							return fmt.Errorf("no previous release to roll back to (only %d release found)", len(releases))
						}
						releaseID = releases[1] // [0]=latest, [1]=previous
						fmt.Printf("auto-selected previous release: %s\n", releaseID)
					}

					// Preflight: verify presence and consistency.
					pf := releasePreflight(conn, releaseID, relDir, localDir)
					fmt.Printf("preflight: %s\n", pf.summary)
					switch pf.action {
					case preflightStop:
						return fmt.Errorf("rollback aborted: %s", pf.reason)
					case preflightWarn:
						if !gf.Yes {
							fmt.Printf("warning: %s\nProceed? [y/N] ", pf.reason)
							var ans string
							fmt.Scan(&ans)
							if strings.ToLower(ans) != "y" {
								return fmt.Errorf("rollback cancelled")
							}
						}
						// Local-only: re-upload artifact files using paths stored in the local manifest.
						return reuploadFromLocal(conn, releaseID, localDir, tgt)
					}

					// Normal path: remote archive exists — copy back to target dir on server.
					relPath := fmt.Sprintf("%s/%s", relDir, releaseID)
					_, stderr, code, err := conn.Run(fmt.Sprintf("cp -rp %s/. %s/", relPath, tgt.Dir))
					if err != nil || code != 0 {
						return fmt.Errorf("rollback copy failed (exit %d): %s", code, stderr)
					}
					fmt.Printf("rolled back to %s\n", releaseID)
					return nil
				})
			},
		}
		c.Flags().StringVar(&releaseFlag, "release", "", "release ID to roll back to")
		return c
	}())

	// status — compare local snapshot dir vs remote release archive.
	rel.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "compare local snapshots vs remote release archive",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConn(gf, func(conn *regssh.Conn, tgt config.Target, cfg *config.Config) error {
				relDir := cfg.Release.Dir
				if relDir == "" {
					return fmt.Errorf("release.dir not configured")
				}
				localDir := effectiveLocalDir(cfg)

				stdout, _, _, err := conn.Run(fmt.Sprintf(
					"cd %s && ls -dt v* 2>/dev/null || true", relDir,
				))
				if err != nil {
					return fmt.Errorf("list remote releases: %w", err)
				}
				remoteSet := map[string]bool{}
				for _, r := range strings.Fields(strings.TrimSpace(stdout)) {
					remoteSet[r] = true
				}

				localSet := map[string]bool{}
				if entries, readErr := os.ReadDir(localDir); readErr == nil {
					for _, e := range entries {
						if e.IsDir() {
							localSet[e.Name()] = true
						}
					}
				}

				if len(remoteSet) == 0 && len(localSet) == 0 {
					fmt.Println("no releases found (remote or local)")
					return nil
				}

				allKeys := map[string]bool{}
				for k := range remoteSet {
					allKeys[k] = true
				}
				for k := range localSet {
					allKeys[k] = true
				}
				keys := make([]string, 0, len(allKeys))
				for k := range allKeys {
					keys = append(keys, k)
				}
				sort.Sort(sort.Reverse(sort.StringSlice(keys)))

				fmt.Printf("status  %s  (local: %s)\n", tgt.Name, localDir)
				fmt.Println(strings.Repeat("─", 60))

				var bothOK, bothDiff, remoteOnly, localOnly int
				for _, id := range keys {
					r, l := remoteSet[id], localSet[id]
					switch {
					case r && !l:
						fmt.Printf("  R  %s\n", id)
						remoteOnly++
					case !r && l:
						fmt.Printf("  L  %s\n", id)
						localOnly++
					default:
						pf := releasePreflight(conn, id, relDir, localDir)
						if pf.diverged {
							fmt.Printf("  !  %s  (%s)\n", id, pf.reason)
							bothDiff++
						} else {
							fmt.Printf("  =  %s\n", id)
							bothOK++
						}
					}
				}

				fmt.Println(strings.Repeat("─", 60))
				var parts []string
				if bothOK > 0 {
					parts = append(parts, fmt.Sprintf("%d in sync", bothOK))
				}
				if bothDiff > 0 {
					parts = append(parts, fmt.Sprintf("%d mismatched", bothDiff))
				}
				if remoteOnly > 0 {
					parts = append(parts, fmt.Sprintf("%d remote-only", remoteOnly))
				}
				if localOnly > 0 {
					parts = append(parts, fmt.Sprintf("%d local-only", localOnly))
				}
				fmt.Println(strings.Join(parts, " · "))
				return nil
			})
		},
	})

	// rdiff — compare hashs between two release manifests.
	rel.AddCommand(&cobra.Command{
		Use:   "rdiff [id1] [id2]",
		Short: "compare hashs between two release manifests",
		Long: `rdiff compares the artifact hashs recorded in two release manifests.

  No args:   compare the two most recent releases (latest vs previous).
  One arg:   compare id1 vs the most recent release.
  Two args:  compare id1 vs id2 (id1=older, id2=newer).`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConn(gf, func(conn *regssh.Conn, tgt config.Target, cfg *config.Config) error {
				relDir := cfg.Release.Dir
				if relDir == "" {
					return fmt.Errorf("release.dir not configured")
				}

				stdout, _, _, err := conn.Run(fmt.Sprintf(
					"cd %s && ls -dt v* 2>/dev/null || true", relDir,
				))
				if err != nil {
					return fmt.Errorf("list releases: %w", err)
				}
				releases := strings.Fields(strings.TrimSpace(stdout))
				if len(releases) < 1 {
					return fmt.Errorf("no releases found in %s", relDir)
				}

				var idA, idB string
				switch len(args) {
				case 0:
					if len(releases) < 2 {
						return fmt.Errorf("need at least 2 releases to diff; only %d found", len(releases))
					}
					idA, idB = releases[1], releases[0]
				case 1:
					idA, idB = args[0], releases[0]
				case 2:
					idA, idB = args[0], args[1]
				}

				loadRemoteManifest := func(id string) (runner.ReleaseManifest, error) {
					data, dlErr := conn.Download(fmt.Sprintf("%s/%s/.regis-release", relDir, id))
					if dlErr != nil {
						return runner.ReleaseManifest{}, fmt.Errorf("download manifest for %s: %w", id, dlErr)
					}
					var m runner.ReleaseManifest
					if parseErr := yaml.Unmarshal(data, &m); parseErr != nil {
						return runner.ReleaseManifest{}, fmt.Errorf("parse manifest for %s: %w", id, parseErr)
					}
					return m, nil
				}

				mA, err := loadRemoteManifest(idA)
				if err != nil {
					return err
				}
				mB, err := loadRemoteManifest(idB)
				if err != nil {
					return err
				}

				fmt.Printf("rdiff  %s  →  %s\n", idA, idB)
				fmt.Println(strings.Repeat("─", 60))

				type entry struct{ a, b string }
				cues := make(map[string]entry)
				for k, v := range mA.Hashes {
					e := cues[k]
					e.a = v
					cues[k] = e
				}
				for k, v := range mB.Hashes {
					e := cues[k]
					e.b = v
					cues[k] = e
				}

				if len(cues) == 0 {
					fmt.Println("  no hash data in either manifest")
					fmt.Println(strings.Repeat("─", 60))
					return nil
				}

				cueKeys := make([]string, 0, len(cues))
				for k := range cues {
					cueKeys = append(cueKeys, k)
				}
				sort.Strings(cueKeys)

				var added, removed, changed, equal int
				for _, name := range cueKeys {
					e := cues[name]
					switch {
					case e.a == "" && e.b != "":
						fmt.Printf("  +  %-24s (new)\n", name)
						added++
					case e.a != "" && e.b == "":
						fmt.Printf("  -  %-24s (removed)\n", name)
						removed++
					case e.a != e.b:
						fmt.Printf("  ~  %-24s %s → %s\n", name, shortHash(e.a), shortHash(e.b))
						changed++
					default:
						fmt.Printf("  =  %-24s %s\n", name, shortHash(e.a))
						equal++
					}
				}
				fmt.Println(strings.Repeat("─", 60))
				var parts []string
				if changed > 0 {
					parts = append(parts, fmt.Sprintf("%d changed", changed))
				}
				if added > 0 {
					parts = append(parts, fmt.Sprintf("%d added", added))
				}
				if removed > 0 {
					parts = append(parts, fmt.Sprintf("%d removed", removed))
				}
				if equal > 0 {
					parts = append(parts, fmt.Sprintf("%d unchanged", equal))
				}
				fmt.Println(strings.Join(parts, " · "))
				return nil
			})
		},
	})

	return rel
}

// releaseConn is the SSH connection subset used by release helper functions.
// *regssh.Conn satisfies this interface; tests use a mock.
type releaseConn interface {
	Run(cmd string) (stdout, stderr string, exitCode int, err error)
	Download(remotePath string) ([]byte, error)
	Upload(localPath, remotePath string, mode fs.FileMode, useSudo bool) error
	UploadBytes(data []byte, remotePath string, mode fs.FileMode, useSudo bool) error
}

// preflightDecision is the rollback gate returned by releasePreflight.
type preflightDecision int

const (
	preflightOK   preflightDecision = iota
	preflightWarn                   // proceed only with explicit confirmation
	preflightStop                   // hard abort
)

// preflightState holds the result of the preflight check for one release.
type preflightState struct {
	action       preflightDecision
	remoteExists bool
	localExists  bool
	diverged     bool
	summary      string // one-line shown to the user
	reason       string // detail for warn/stop
}

// releasePreflight checks presence and consistency across remote archive and local snapshot.
func releasePreflight(conn releaseConn, releaseID, remoteDir, localDir string) preflightState {
	_, _, code, _ := conn.Run(fmt.Sprintf("test -d %s/%s", remoteDir, releaseID))
	remoteExists := code == 0

	_, statErr := os.Stat(filepath.Join(localDir, releaseID))
	localExists := statErr == nil

	switch {
	case !remoteExists && !localExists:
		return preflightState{
			action:  preflightStop,
			summary: "not found anywhere",
			reason:  fmt.Sprintf("release %q not found in remote archive (%s) or local snapshot (%s)", releaseID, remoteDir, localDir),
		}
	case remoteExists && !localExists:
		return preflightState{
			action:       preflightOK,
			remoteExists: true,
			summary:      "remote archive only (no local snapshot)",
		}
	case !remoteExists && localExists:
		return preflightState{
			action:      preflightWarn,
			localExists: true,
			summary:     "local snapshot only — remote archive not found",
			reason:      "remote archive missing; rollback will re-upload from local snapshot using stored artifact paths",
		}
	default:
		// Both present: compare manifests.
		remoteM, localM, ok := loadBothManifests(conn, releaseID, remoteDir, localDir)
		if !ok {
			return preflightState{
				action:       preflightOK,
				remoteExists: true,
				localExists:  true,
				summary:      "both present (manifest comparison skipped)",
			}
		}
		if !hashesEqual(remoteM.Hashes, localM.Hashes) {
			return preflightState{
				action:       preflightStop,
				remoteExists: true,
				localExists:  true,
				diverged:     true,
				summary:      "mismatch",
				reason: fmt.Sprintf(
					"hash mismatch — remote deployed_at %s, local deployed_at %s",
					remoteM.DeployedAt.Format("2006-01-02 15:04:05"),
					localM.DeployedAt.Format("2006-01-02 15:04:05"),
				),
			}
		}
		return preflightState{
			action:       preflightOK,
			remoteExists: true,
			localExists:  true,
			summary:      "remote + local verified (hashs match)",
		}
	}
}

// loadBothManifests reads .regis-release from both the remote archive and the local snapshot.
func loadBothManifests(conn releaseConn, releaseID, remoteDir, localDir string) (remote, local runner.ReleaseManifest, ok bool) {
	remoteData, err := conn.Download(fmt.Sprintf("%s/%s/.regis-release", remoteDir, releaseID))
	if err != nil {
		return
	}
	localData, err := os.ReadFile(filepath.Join(localDir, releaseID, ".regis-release"))
	if err != nil {
		return
	}
	if yaml.Unmarshal(remoteData, &remote) != nil || yaml.Unmarshal(localData, &local) != nil {
		return
	}
	ok = true
	return
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

// reuploadFromLocal re-deploys a release from the local snapshot.
// The manifest's Artifacts map (cue name → remote path) provides the upload destinations.
func reuploadFromLocal(conn releaseConn, releaseID, localDir string, tgt config.Target) error {
	snapshotDir := filepath.Join(localDir, releaseID)
	manifestData, err := os.ReadFile(filepath.Join(snapshotDir, ".regis-release"))
	if err != nil {
		return fmt.Errorf("read local manifest: %w", err)
	}
	var m runner.ReleaseManifest
	if err := yaml.Unmarshal(manifestData, &m); err != nil {
		return fmt.Errorf("parse local manifest: %w", err)
	}
	if len(m.Artifacts) == 0 {
		return fmt.Errorf("local manifest has no artifact paths — cannot re-upload")
	}
	for cueName, remotePath := range m.Artifacts {
		localFile := filepath.Join(snapshotDir, cueName)
		if err := conn.Upload(localFile, remotePath, fs.FileMode(0644), tgt.Sudo); err != nil {
			return fmt.Errorf("upload %s → %s: %w", cueName, remotePath, err)
		}
		fmt.Printf("  uploaded %s → %s\n", cueName, remotePath)
	}
	if err := conn.UploadBytes(manifestData, tgt.Dir+"/.regis-release", 0644, tgt.Sudo); err != nil {
		fmt.Printf("  warn: could not update .regis-release: %v\n", err)
	}
	fmt.Printf("rolled back to %s (from local snapshot)\n", releaseID)
	return nil
}

// effectiveLocalDir returns the local snapshot directory, defaulting to .regis-releases.
func effectiveLocalDir(cfg *config.Config) string {
	if cfg.Release.LocalDir != "" {
		return cfg.Release.LocalDir
	}
	return ".regis-releases"
}

// shortHash returns the first 8 characters of a hash string.
func shortHash(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

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
	return fn(conn, tgt, cfg)
}
