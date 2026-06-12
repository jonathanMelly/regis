// cmd/regis/cmd/release.go
package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
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
				relDir := resolveReleaseDir(cfg, tgt)
				stdout, _, _, err := conn.Run(fmt.Sprintf(
					"cd %s && ls -dt v* 2>/dev/null || true", relDir,
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
					relDir := resolveReleaseDir(cfg, tgt)
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
				relDir := resolveReleaseDir(cfg, tgt)
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
				relDir := resolveReleaseDir(cfg, tgt)

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

	// check — compare a manifest's recorded hashes against actual remote files.
	rel.AddCommand(func() *cobra.Command {
		var rebuild bool
		var remove bool
		c := &cobra.Command{
			Use:   "check [id]",
			Short: "compare a release manifest's hashes against actual remote files",
			Long: `check downloads a manifest and compares its recorded hashes against the
actual files currently on the target.

No arg: checks the live manifest at <target.dir>/.regis-release.
With id: checks a specific historical manifest from the release archive.

  =  hash matches
  ~  hash differs (drift or partial deploy)
  ?  dest exists on target but not tracked in manifest
  -  dest missing on target
  *  render file (exists, hash not tracked by manifest)

Flags:
  --rebuild   hash remote files and write a new accurate manifest (new release ID)
  --remove    delete the live manifest from target (clean slate)`,
			Args: cobra.MaximumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				if rebuild && remove {
					return fmt.Errorf("--rebuild and --remove are mutually exclusive")
				}
				return withConn(gf, func(conn *regssh.Conn, tgt config.Target, cfg *config.Config) error {
					// Load the manifest to check against.
					var manifestData []byte
					var manifestSrc string
					if len(args) == 1 {
						relDir := resolveReleaseDir(cfg, tgt)
						remotePath := fmt.Sprintf("%s/%s/.regis-release", relDir, args[0])
						data, dlErr := conn.Download(remotePath)
						if dlErr != nil {
							return fmt.Errorf("download manifest for %s: %w", args[0], dlErr)
						}
						manifestData = data
						manifestSrc = args[0]
					} else {
						data, dlErr := conn.Download(tgt.Dir + "/.regis-release")
						if dlErr != nil {
							fmt.Println("no live manifest found — target has not been deployed with regis")
							return nil
						}
						manifestData = data
						manifestSrc = "live"
					}

					var m runner.ReleaseManifest
					if err := yaml.Unmarshal(manifestData, &m); err != nil {
						return fmt.Errorf("parse manifest: %w", err)
					}

					fmt.Printf("manifest  %s [%s]  deployed %s by %s\n",
						m.Release, manifestSrc,
						m.DeployedAt.Format("2006-01-02 15:04:05 UTC"),
						m.DeployedBy,
					)

					// checkEntry represents one remote file to verify.
					// baseCueName is the cue name used for CueArtifacts grouping (differs from
					// cueName only for render folder files, e.g. cueName="html/index.html" baseCueName="html").
					// trackHash is true for binary/config/secret — the only natures that record hashes
					// in the manifest (matches normal deploy behaviour).
					type checkEntry struct {
						cueName     string
						baseCueName string
						remotePath  string
						trackHash   bool
					}
					var entries []checkEntry
					seen := map[string]bool{}

					for _, scName := range cfg.ScenarioNames {
						sc := cfg.Scenarios[scName]
						for _, cr := range sc.Cues {
							if cr.ScenarioRef != "" || cr.Dest == "" {
								continue
							}
							if seen[cr.Name] {
								continue
							}
							seen[cr.Name] = true
							switch cr.Nature {
							case "binary", "config", "secret":
								entries = append(entries, checkEntry{
									cueName:     cr.Name,
									baseCueName: cr.Name,
									remotePath:  resolveRemotePathFetch(cr.Dest, tgt.Dir),
									trackHash:   true,
								})
							case "render":
								// Folder render: walk LocalDest to enumerate per-file remote paths.
								if cr.LocalDest != "" {
									if info, statErr := os.Stat(cr.LocalDest); statErr == nil && info.IsDir() {
										remoteBase := resolveRemotePathFetch(strings.TrimRight(cr.Dest, "/"), tgt.Dir)
										_ = filepath.WalkDir(cr.LocalDest, func(p string, d fs.DirEntry, err error) error {
											if err != nil || d.IsDir() {
												return err
											}
											rel, _ := filepath.Rel(cr.LocalDest, p)
											relFwd := filepath.ToSlash(rel)
											entries = append(entries, checkEntry{
												cueName:     cr.Name + "/" + relFwd,
												baseCueName: cr.Name,
												remotePath:  remoteBase + "/" + relFwd,
											})
											return nil
										})
										continue
									}
								}
								// Single-file render.
								entries = append(entries, checkEntry{
									cueName:     cr.Name,
									baseCueName: cr.Name,
									remotePath:  resolveRemotePathFetch(cr.Dest, tgt.Dir),
								})
							}
						}
					}
					// Also include manifest artifacts not covered by config (removed cues, etc.)
					// Preserve trackHash for cues the manifest originally hashed.
					for cueName, remotePath := range m.Artifacts {
						if seen[cueName] {
							continue
						}
						seen[cueName] = true
						entries = append(entries, checkEntry{
							cueName:     cueName,
							baseCueName: cueName,
							remotePath:  remotePath,
							trackHash:   m.Hashes[cueName] != "",
						})
					}

					// Hash all remote paths in one bulk call.
					remotePaths := make([]string, len(entries))
					for i, e := range entries {
						remotePaths[i] = e.remotePath
					}
					remoteHashes := cue.BulkHashRemote(conn, remotePaths)

					fmt.Printf("checking %d files...\n", len(entries))
					fmt.Println(strings.Repeat("─", 60))

					var nMatch, nDrift, nUntracked, nRender, nMissing int
					// For rebuild: collect accurate hashes and properly grouped artifact paths.
					newHashes := make(map[string]string)
					newArtifacts := make(map[string]string)
					newCueArtifacts := make(map[string]map[string]string)

					for _, e := range entries {
						manifestHash := m.Hashes[e.cueName]
						remoteHash, exists := remoteHashes[e.remotePath]

						// Always record artifact paths for present files (used by rollback).
						if exists && e.remotePath != "" {
							newArtifacts[e.cueName] = e.remotePath
							if newCueArtifacts[e.baseCueName] == nil {
								newCueArtifacts[e.baseCueName] = make(map[string]string)
							}
							newCueArtifacts[e.baseCueName][e.cueName] = e.remotePath
						}

						switch {
						case !exists:
							fmt.Printf("  -  %-28s (missing on target)\n", e.cueName)
							nMissing++
						case !e.trackHash:
							// Render files: manifest does not record hashes, just verify presence.
							fmt.Printf("  *  %-28s %s\n", e.cueName, shortHash(remoteHash))
							nRender++
						case manifestHash == "":
							fmt.Printf("  ?  %-28s %s (not in manifest)\n", e.cueName, shortHash(remoteHash))
							newHashes[e.cueName] = remoteHash
							nUntracked++
						case remoteHash == manifestHash:
							fmt.Printf("  =  %-28s %s\n", e.cueName, shortHash(remoteHash))
							newHashes[e.cueName] = remoteHash
							nMatch++
						default:
							fmt.Printf("  ~  %-28s remote:%s  manifest:%s\n",
								e.cueName, shortHash(remoteHash), shortHash(manifestHash))
							newHashes[e.cueName] = remoteHash
							nDrift++
						}
					}

					fmt.Println(strings.Repeat("─", 60))
					var parts []string
					if nMatch > 0 {
						parts = append(parts, fmt.Sprintf("%d match", nMatch))
					}
					if nDrift > 0 {
						parts = append(parts, fmt.Sprintf("%d mismatch", nDrift))
					}
					if nUntracked > 0 {
						parts = append(parts, fmt.Sprintf("%d untracked", nUntracked))
					}
					if nRender > 0 {
						parts = append(parts, fmt.Sprintf("%d render", nRender))
					}
					if nMissing > 0 {
						parts = append(parts, fmt.Sprintf("%d missing", nMissing))
					}
					fmt.Println(strings.Join(parts, " · "))

					isConsistent := nDrift == 0 && nUntracked == 0 && nMissing == 0
					if !isConsistent && !rebuild && !remove {
						fmt.Println("\nmanifest is stale — run with --rebuild to refresh or --remove to clear it")
					}

					if remove {
						_, _, code, err := conn.Run("rm -f " + tgt.Dir + "/.regis-release")
						if err != nil || code != 0 {
							return fmt.Errorf("remove manifest failed (exit %d)", code)
						}
						fmt.Println("\nremoved live manifest")
						return nil
					}

					if rebuild {
						releaseID := runner.NewReleaseID()
						hostname, _ := os.Hostname()
						user := os.Getenv("USER")
						if user == "" {
							user = os.Getenv("USERNAME")
						}

						if len(newHashes) == 0 {
							newHashes = nil
						}
						if len(newArtifacts) == 0 {
							newArtifacts = nil
						}
						if len(newCueArtifacts) == 0 {
							newCueArtifacts = nil
						}

						rebuilt := runner.ReleaseManifest{
							Release:      releaseID,
							DeployedAt:   time.Now().UTC(),
							DeployedBy:   user + "@" + hostname,
							Scenarios:    cfg.ScenarioNames,
							Hashes:       newHashes,
							Artifacts:    newArtifacts,
							CueArtifacts: newCueArtifacts,
						}

						if err := runner.WriteManifest(conn, tgt.Dir, rebuilt, tgt.Sudo); err != nil {
							return fmt.Errorf("write manifest: %w", err)
						}
						fmt.Printf("\nmanifest  %s\n", releaseID)

						localDir := effectiveLocalDir(cfg)
						raw, _ := yaml.Marshal(rebuilt)
						runner.WriteSnapshot(localDir, releaseID, raw, nil)
						fmt.Printf("snapshot  %s/%s\n", localDir, releaseID)

						relDir := resolveReleaseDir(cfg, tgt)
						archiveCmd := fmt.Sprintf("mkdir -p %s && cp -rp %s/. %s/%s/",
							relDir, tgt.Dir, relDir, releaseID)
						if _, stderr, code, archErr := conn.Run(archiveCmd); archErr != nil || code != 0 {
							fmt.Fprintf(os.Stderr, "warn: archive failed (exit %d): %s\n", code, stderr)
						} else {
							fmt.Printf("archive   %s/%s\n", relDir, releaseID)
						}

						fmt.Println("\nnote: rebuild is best-effort — hashes reflect remote files at check time.")
						fmt.Println("      if local sources diverge from target, run 'regis run' for an accurate manifest.")
					}

					return nil
				})
			},
		}
		c.Flags().BoolVar(&rebuild, "rebuild", false, "hash remote files and write a new accurate manifest")
		c.Flags().BoolVar(&remove, "remove", false, "delete the live manifest from target")
		return c
	}())

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

// resolveReleaseDir returns cfg.Release.Dir when set, or falls back to
// <tgt.Dir>/.regis-releases — matching the default used by the runner.
func resolveReleaseDir(cfg *config.Config, tgt config.Target) string {
	if cfg.Release.Dir != "" {
		return cfg.Release.Dir
	}
	return path.Join(tgt.Dir, ".regis-releases")
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
