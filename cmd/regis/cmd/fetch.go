// cmd/regis/cmd/fetch.go
package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/runner"
	regssh "git.disroot.org/jmy/regis/internal/ssh"
)

func newFetchCommand(gf *GlobalFlags) *cobra.Command {
	var archive bool
	cmd := &cobra.Command{
		Use:   "fetch",
		Short: "download remote artifacts to local source paths (or .regis/fetched/ with --archive)",
		Long: `fetch downloads the current remote state of each cue's artifact.

Default: writes directly to local source paths (src for binary/config/secret, local_dest for render).
  - If the local path already exists, fetch notifies and skips it.
  - Use --archive to save to .regis/fetched/ without touching local files.

Also bootstraps the local release archive (.regis-releases/) if release.dir is configured,
so 'release list' and future rollbacks work on a fresh clone.

Useful for disaster recovery, new machine setup, or reverse-engineering a deployment.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(gf.File)
			if err != nil {
				return err
			}
			var targetNames []string
			for _, t := range cfg.Targets {
				targetNames = append(targetNames, t.Name)
			}
			selected := SelectTargets(targetNames, gf.Target)

			for _, tgtName := range selected {
				var tgt config.Target
				for _, t := range cfg.Targets {
					if t.Name == tgtName {
						tgt = t
					}
				}
				func() {
					conn, err := regssh.Dial(tgt)
					if err != nil {
						fmt.Fprintf(os.Stderr, "connect %s: %v\n", tgtName, err)
						return
					}
					defer conn.Close()

					// Bootstrap local release archive: read the remote manifest to get the
					// release ID so we can create a matching local snapshot entry.
					var snapshotID string
					var snapshotManifestRaw []byte
					snapshotFiles := map[string][]byte{}
					if cfg.Release.Dir != "" {
						if raw, dlErr := conn.Download(tgt.Dir + "/.regis-release"); dlErr == nil {
							var rm runner.ReleaseManifest
							if yaml.Unmarshal(raw, &rm) == nil && rm.Release != "" {
								snapshotID = rm.Release
								snapshotManifestRaw = raw
							}
						}
					}

					for scName, sc := range cfg.Scenarios {
						_ = scName
						for _, cr := range sc.Cues {
							if cr.ScenarioRef != "" || cr.Nature == "action" || cr.Nature == "generate" {
								continue
							}
							if cr.Dest == "" {
								continue
							}

							remotePath := resolveRemotePathFetch(cr.Dest, tgt.Dir)
							data, err := conn.Download(remotePath)
							if err != nil {
								fmt.Fprintf(os.Stderr, "fetch %s: %v\n", remotePath, err)
								continue
							}

							// Collect for release snapshot (keyed by cue name).
							if snapshotID != "" {
								snapshotFiles[cr.Name] = data
							}

							if archive {
								fetchDir := filepath.Join(".regis", "fetched", tgtName)
								os.MkdirAll(fetchDir, 0755)
								dest := filepath.Join(fetchDir, filepath.Base(remotePath))
								if err := os.WriteFile(dest, data, 0644); err != nil {
									fmt.Fprintf(os.Stderr, "write %s: %v\n", dest, err)
									continue
								}
								fmt.Printf("archived %s → %s\n", remotePath, dest)
							} else {
								localPath := FetchLocalPath(cr)
								if localPath == "" {
									fmt.Printf("skip %s: no local path configured (set src or local_dest)\n", cr.Name)
									continue
								}
								if _, statErr := os.Stat(localPath); statErr == nil {
									fmt.Printf("skip %s: %s already exists\n", cr.Name, localPath)
									continue
								}
								if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
									fmt.Fprintf(os.Stderr, "mkdir %s: %v\n", filepath.Dir(localPath), err)
									continue
								}
								if err := os.WriteFile(localPath, data, 0644); err != nil {
									fmt.Fprintf(os.Stderr, "write %s: %v\n", localPath, err)
									continue
								}
								fmt.Printf("fetched %s → %s\n", remotePath, localPath)

								// Run reverse shell if configured (render only).
								if cr.Reverse != "" {
									if err := RunReverseShell(cr.Reverse, localPath); err != nil {
										fmt.Fprintf(os.Stderr, "reverse %s: %v\n", cr.Name, err)
									}
								}
							}
						}
					}

					// Write release snapshot from the collected files.
					if snapshotID != "" {
						localDir := cfg.Release.LocalDir
					if localDir == "" {
						localDir = ".regis-releases"
					}
					runner.WriteSnapshot(localDir, snapshotID, snapshotManifestRaw, snapshotFiles)
						fmt.Printf("snapshot  %s/%s  (%d files)\n", cfg.Release.Dir, snapshotID, len(snapshotFiles))
					}
				}()
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&archive, "archive", false, "save to .regis/fetched/<target>/ instead of local paths")
	return cmd
}

// FetchLocalPath returns the local destination for a fetched artifact.
// render → local_dest; binary/config/secret → src[0].
func FetchLocalPath(cr config.CueRef) string {
	if cr.Nature == "render" {
		return cr.LocalDest
	}
	if len(cr.Src) > 0 {
		return cr.Src[0]
	}
	return ""
}

// resolveRemotePathFetch resolves dest relative to targetDir for a remote Linux path.
// Uses path.Join (forward slashes) rather than filepath.Join to stay correct on Windows.
func resolveRemotePathFetch(dest, targetDir string) string {
	if path.IsAbs(dest) {
		return dest
	}
	return path.Join(targetDir, dest)
}

// RunReverseShell runs the reverse shell with ARTIFACT_PATH pointing to the fetched file.
func RunReverseShell(shell, artifactPath string) error {
	var cmd *exec.Cmd
	if os.PathSeparator == '\\' {
		cmd = exec.Command("powershell", "-NoProfile", "-Command", shell)
	} else {
		cmd = exec.Command("sh", "-c", shell)
	}
	cmd.Env = append(os.Environ(), "ARTIFACT_PATH="+artifactPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
