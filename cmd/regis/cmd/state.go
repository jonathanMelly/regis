// cmd/regis/cmd/state.go
package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
	"git.disroot.org/jmy/regis/internal/runner"
	regssh "git.disroot.org/jmy/regis/internal/ssh"
)

func newStateCommand(gf *GlobalFlags) *cobra.Command {
	st := &cobra.Command{
		Use:   "state",
		Short: "inspect and verify deployment state",
		Long: `regis state — commands for inspecting what regis last deployed.

State records the git ref, per-cue file inventory (remote paths + hashes),
and deployment metadata. Used for drift detection and recovery guidance.`,
	}
	st.AddCommand(newStateShowCommand(gf))
	st.AddCommand(newStateListCommand(gf))
	st.AddCommand(newStateCheckCommand(gf))
	st.AddCommand(newStateAdoptCommand(gf))
	st.AddCommand(newStateHintCommand(gf))
	return st
}

// ── state show ────────────────────────────────────────────────────────────────

func newStateShowCommand(gf *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "show the live deployment state on the target",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConn(gf, func(conn *regssh.Conn, tgt config.Target, cfg *config.Config) error {
				state, err := runner.LoadRemoteState(conn, tgt.Dir)
				if err != nil {
					fmt.Println("no state found — target has not been deployed with regis")
					return nil
				}
				printStateSummary(state, tgt.Name)
				return nil
			})
		},
	}
}

func printStateSummary(s *runner.State, target string) {
	fmt.Printf("state     %s\n", s.ID)
	fmt.Printf("target    %s\n", target)
	if s.GitRef != "" {
		clean := ""
		if !s.GitClean {
			clean = "  (dirty tree at deploy time)"
		}
		fmt.Printf("git_ref   %s%s\n", shortRef(s.GitRef), clean)
	}
	fmt.Printf("deployed  %s  by %s\n", s.DeployedAt.Format("2006-01-02 15:04:05 UTC"), s.DeployedBy)
	if len(s.Scenarios) > 0 {
		fmt.Printf("scenarios %s\n", strings.Join(s.Scenarios, ", "))
	}
	if len(s.Cues) == 0 {
		return
	}
	fmt.Printf("\ncues (%d)\n", len(s.Cues))
	for cueName, cs := range s.Cues {
		fmt.Printf("  %-20s  %s  %d file(s)\n", cueName, cs.Nature, len(cs.Files))
	}
}

func shortRef(ref string) string {
	if len(ref) > 12 {
		return ref[:12]
	}
	return ref
}

// ── state list ────────────────────────────────────────────────────────────────

func newStateListCommand(gf *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "list local deployment states for this target",
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
			if len(selected) == 0 {
				return fmt.Errorf("no targets matched")
			}
			localDir := cfg.State.LocalDir
			if localDir == "" {
				localDir = ".regis-states"
			}
			for _, tgtName := range selected {
				ids := runner.ListLocalStates(localDir, tgtName)
				if len(ids) == 0 {
					fmt.Printf("no local states for %s\n", tgtName)
					continue
				}
				fmt.Printf("states  %s  (local: %s/%s)\n", tgtName, localDir, tgtName)
				for _, id := range ids {
					s, err := runner.LoadLocalState(localDir, tgtName, id)
					if err != nil {
						fmt.Printf("  %s  (unreadable)\n", id)
						continue
					}
					dirty := ""
					if !s.GitClean && s.GitRef != "" {
						dirty = " !"
					}
					ref := ""
					if s.GitRef != "" {
						ref = "  " + shortRef(s.GitRef) + dirty
					}
					fmt.Printf("  %s  %s  by %s%s\n",
						id,
						s.DeployedAt.Format("2006-01-02 15:04"),
						s.DeployedBy,
						ref,
					)
				}
			}
			return nil
		},
	}
}

// ── state check ───────────────────────────────────────────────────────────────

func newStateCheckCommand(gf *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "check [state-id]",
		Short: "verify target files against the recorded deployment state",
		Long: `check downloads the live state and compares each tracked file's
mtime+size against the remote. Files where mtime/size differ are
re-hashed for confirmation.

Symbols:
  =  in sync (matches deployed state)
  ~  content drifted since last deploy
  -  missing on target
  ?  no hash recorded (first deploy or equal via fast-path)

Resolution: run rdiff to understand changes, then redeploy.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConn(gf, func(conn *regssh.Conn, tgt config.Target, cfg *config.Config) error {
				// Load state to check against.
				var state *runner.State
				var err error
				if len(args) == 1 {
					localDir := cfg.State.LocalDir
					if localDir == "" {
						localDir = ".regis-states"
					}
					state, err = runner.LoadLocalState(localDir, tgt.Name, args[0])
					if err != nil {
						return fmt.Errorf("load state %s: %w", args[0], err)
					}
				} else {
					state, err = runner.LoadRemoteState(conn, tgt.Dir)
					if err != nil {
						fmt.Println("no live state found — target has not been deployed with regis")
						fmt.Println("run 'regis state adopt' to create a state from current remote")
						return nil
					}
				}

				fmt.Printf("checking  %s  [%s]  deployed %s by %s\n",
					tgt.Name, state.ID,
					state.DeployedAt.Format("2006-01-02 15:04:05 UTC"),
					state.DeployedBy,
				)
				fmt.Println(strings.Repeat("─", 60))

				// Collect all remote paths to stat in one bulk call.
				type entry struct {
					cueName  string
					relKey   string
					nature   string
					fileState runner.FileState
				}
				var entries []entry
				for cueName, cs := range state.Cues {
					for relKey, fs := range cs.Files {
						entries = append(entries, entry{cueName, relKey, cs.Nature, fs})
					}
				}

				// Bulk stat remote files (mtime+size fast path).
				remotePaths := make([]string, len(entries))
				for i, e := range entries {
					remotePaths[i] = e.fileState.Remote
				}
				remoteStats := cue.BulkStatRemote(conn, remotePaths)

				// Identify files needing hash verification (mtime or size changed).
				var hashPaths []string
				needsHash := make(map[string]bool)
				for _, e := range entries {
					st := remoteStats[e.fileState.Remote]
					if st.Size < 0 {
						continue // missing — no need to hash
					}
					stateHasStats := e.fileState.Mtime != 0 || e.fileState.Size != 0
					if !stateHasStats {
						// No fast-path data: hash everything with a stored hash.
						if e.fileState.Hash != "" && e.nature != "secret" {
							hashPaths = append(hashPaths, e.fileState.Remote)
							needsHash[e.fileState.Remote] = true
						}
						continue
					}
					if e.fileState.Mtime != 0 && st.Mtime.Unix() != e.fileState.Mtime {
						hashPaths = append(hashPaths, e.fileState.Remote)
						needsHash[e.fileState.Remote] = true
						continue
					}
					if e.fileState.Size != 0 && st.Size != e.fileState.Size {
						hashPaths = append(hashPaths, e.fileState.Remote)
						needsHash[e.fileState.Remote] = true
					}
				}
				remoteHashes := cue.BulkHashRemote(conn, hashPaths)

				var nSync, nDrift, nMissing, nNoHash int
				for _, e := range entries {
					st := remoteStats[e.fileState.Remote]
					if st.Size < 0 {
						fmt.Printf("  -  %s/%s  (missing on target)\n", e.cueName, e.relKey)
						nMissing++
						continue
					}
					// Secret: no hash check, just verify presence.
					if e.nature == "secret" {
						fmt.Printf("  =  %s/%s  (present — secret, content not verified)\n", e.cueName, e.relKey)
						nSync++
						continue
					}
					if e.fileState.Hash == "" {
						fmt.Printf("  ?  %s/%s  (no hash recorded)\n", e.cueName, e.relKey)
						nNoHash++
						continue
					}
					if !needsHash[e.fileState.Remote] {
						// mtime+size matched: fast-path equal.
						fmt.Printf("  =  %s/%s\n", e.cueName, e.relKey)
						nSync++
						continue
					}
					remoteHash := remoteHashes[e.fileState.Remote]
					if remoteHash == e.fileState.Hash {
						fmt.Printf("  =  %s/%s\n", e.cueName, e.relKey)
						nSync++
					} else {
						fmt.Printf("  ~  %s/%s  (drifted — deployed:%s  now:%s)\n",
							e.cueName, e.relKey,
							shortHash(e.fileState.Hash),
							shortHash(remoteHash),
						)
						nDrift++
					}
				}

				fmt.Println(strings.Repeat("─", 60))
				var parts []string
				if nSync > 0 {
					parts = append(parts, fmt.Sprintf("%d in sync", nSync))
				}
				if nDrift > 0 {
					parts = append(parts, fmt.Sprintf("%d drifted", nDrift))
				}
				if nMissing > 0 {
					parts = append(parts, fmt.Sprintf("%d missing", nMissing))
				}
				if nNoHash > 0 {
					parts = append(parts, fmt.Sprintf("%d unverified", nNoHash))
				}
				fmt.Println(strings.Join(parts, " · "))

				if nDrift > 0 || nMissing > 0 {
					fmt.Println("\ntarget has drifted from last deploy")
					fmt.Println("→ run rdiff to understand, then redeploy")
				}
				return nil
			})
		},
	}
}

// ── state adopt ───────────────────────────────────────────────────────────────

func newStateAdoptCommand(gf *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "adopt",
		Short: "create a state record from the current remote (no deploy)",
		Long: `adopt hashes remote files at cue destinations and writes a state record.
Use when target has content but no prior state — enables prune and drift
detection on subsequent deploys.

pack and render folder cues cannot be fully enumerated without prior
state data; they are skipped with a note.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConn(gf, func(conn *regssh.Conn, tgt config.Target, cfg *config.Config) error {
				localDir := cfg.State.LocalDir
				if localDir == "" {
					localDir = ".regis-states"
				}

				// Collect single-file cue destinations from config.
				type adoptEntry struct {
					cueName    string
					nature     string
					remotePath string
				}
				var entries []adoptEntry
				seen := map[string]bool{}

				for _, scName := range cfg.ScenarioNames {
					sc := cfg.Scenarios[scName]
					for _, cr := range sc.Cues {
						if cr.ScenarioRef != "" || cr.Dest == "" || seen[cr.Name] {
							continue
						}
						seen[cr.Name] = true
						switch cr.Nature {
						case "binary", "config", "secret":
							entries = append(entries, adoptEntry{
								cueName:    cr.Name,
								nature:     cr.Nature,
								remotePath: resolveRemotePathFetch(cr.Dest, tgt.Dir),
							})
						case "render":
							if cr.LocalDest == "" { // single-file only
								entries = append(entries, adoptEntry{
									cueName:    cr.Name,
									nature:     cr.Nature,
									remotePath: resolveRemotePathFetch(cr.Dest, tgt.Dir),
								})
							} else {
								fmt.Fprintf(os.Stderr, "note: skipping render folder cue %q — re-deploy to record\n", cr.Name)
							}
						case "pack":
							fmt.Fprintf(os.Stderr, "note: skipping pack cue %q — re-deploy to record\n", cr.Name)
						}
					}
				}

				// Also include any artifacts from existing legacy state.
				if prevState, _ := runner.LoadRemoteState(conn, tgt.Dir); prevState != nil {
					for cueName, cs := range prevState.Cues {
						if seen[cueName] {
							continue
						}
						seen[cueName] = true
						for relKey, fs := range cs.Files {
							entries = append(entries, adoptEntry{
								cueName:    cueName + "/" + relKey,
								nature:     cs.Nature,
								remotePath: fs.Remote,
							})
						}
					}
				}

				fmt.Printf("adopting %d files from %s...\n", len(entries), tgt.Name)

				// Hash all remote files.
				remotePaths := make([]string, len(entries))
				for i, e := range entries {
					remotePaths[i] = e.remotePath
				}
				remoteHashes := cue.BulkHashRemote(conn, remotePaths)

				// Build state from remote hashes.
				id := runner.NewStateID()
				hostname, _ := os.Hostname()
				user := os.Getenv("USER")
				if user == "" {
					user = os.Getenv("USERNAME")
				}
				state := runner.State{
					ID:         id,
					DeployedAt: time.Now().UTC(),
					DeployedBy: user + "@" + hostname,
					Target:     tgt.Name,
					Cues:       make(map[string]runner.CueState),
				}
				for _, e := range entries {
					cueName := e.cueName
					relKey := e.cueName
					if idx := strings.Index(e.cueName, "/"); idx >= 0 {
						cueName = e.cueName[:idx]
						relKey = e.cueName[idx+1:]
					}
					cs := state.Cues[cueName]
					cs.Nature = e.nature
					if cs.Files == nil {
						cs.Files = make(map[string]runner.FileState)
					}
					fs := runner.FileState{Remote: e.remotePath}
					if h := remoteHashes[e.remotePath]; h != "" {
						fs.Hash = h
					}
					cs.Files[relKey] = fs
					state.Cues[cueName] = cs
				}

				if err := runner.SaveState(state, localDir); err != nil {
					return fmt.Errorf("save state: %w", err)
				}
				if err := runner.WriteStateToRemote(conn, tgt.Dir, state, tgt.Sudo); err != nil {
					fmt.Fprintf(os.Stderr, "warn: upload state: %v\n", err)
				}
				fmt.Printf("state  %s  (%d cues adopted)\n", id, len(state.Cues))
				fmt.Println("note: pack/render folder cues require a re-deploy to be fully recorded")
				return nil
			})
		},
	}
}

// ── state hint ────────────────────────────────────────────────────────────────

func newStateHintCommand(gf *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "hint [state-id]",
		Short: "show recovery guidance for a state",
		Long: `hint displays what you need to know to recover to a previous state.
It shows the git ref to deploy and any compensation_hint entries from regis.yml.

No args: hints for the previous state (one before live).
With id: hints for that specific state.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConn(gf, func(conn *regssh.Conn, tgt config.Target, cfg *config.Config) error {
				localDir := cfg.State.LocalDir
				if localDir == "" {
					localDir = ".regis-states"
				}

				var targetState *runner.State
				var err error

				if len(args) == 1 {
					targetState, err = runner.LoadLocalState(localDir, tgt.Name, args[0])
					if err != nil {
						return fmt.Errorf("load state %s: %w", args[0], err)
					}
				} else {
					// Default: previous state (one before live).
					ids := runner.ListLocalStates(localDir, tgt.Name)
					if len(ids) < 2 {
						fmt.Println("no previous state found — this is the first recorded deploy")
						return nil
					}
					targetState, err = runner.LoadLocalState(localDir, tgt.Name, ids[1])
					if err != nil {
						return fmt.Errorf("load previous state: %w", err)
					}
				}

				// Load live state to compute prune candidates.
				liveState, _ := runner.LoadRemoteState(conn, tgt.Dir)

				fmt.Printf("rollback to  %s\n", targetState.ID)
				if targetState.GitRef != "" {
					fmt.Printf("git_ref      %s\n", targetState.GitRef)
					if !targetState.GitClean {
						fmt.Println("             ⚠ working tree was dirty at deploy — git ref may not reproduce exactly")
					}
				}
				fmt.Println()

				// Git worktree deploy command.
				if targetState.GitRef != "" {
					fmt.Println("── deploy command ──────────────────────────────────────")
					fmt.Printf("git worktree add /tmp/regis-rollback %s\n", targetState.GitRef)
					fmt.Printf("regis run --from /tmp/regis-rollback --run-without-check\n")
					fmt.Println("git worktree remove /tmp/regis-rollback")
					fmt.Println()
				}

				// Prune candidates: files in live state not in target state.
				if liveState != nil {
					var pruneAll []string
					for cueName := range liveState.Cues {
						candidates := runner.StatePruneCandidates(liveState, targetState, cueName)
						pruneAll = append(pruneAll, candidates...)
					}
					if len(pruneAll) > 0 {
						fmt.Println("── files added after target state (may need manual removal) ──")
						for _, p := range pruneAll {
							fmt.Printf("  %s\n", p)
						}
						fmt.Println()
					}
				}

				// Per-scenario and per-cue compensation_hint from config.
				var hasHints bool
				for _, scName := range targetState.Scenarios {
					sc, ok := cfg.Scenarios[scName]
					if !ok {
						continue
					}
					hint := fillPlaceholders(sc.CompensationHint, targetState.GitRef)
					if hint != "" {
						if !hasHints {
							fmt.Println("── compensation hints ──────────────────────────────────")
							hasHints = true
						}
						fmt.Printf("[%s] %s\n", scName, hint)
					}
					for _, cr := range sc.Cues {
						cueHint := fillPlaceholders(cr.CompensationHint, targetState.GitRef)
						if cueHint != "" {
							if !hasHints {
								fmt.Println("── compensation hints ──────────────────────────────────")
								hasHints = true
							}
							fmt.Printf("  [%s] %s\n", cr.Name, cueHint)
						}
					}
				}

				fmt.Println("note: regis does not automate rollback — the above is guidance only")
				return nil
			})
		},
	}
}

func fillPlaceholders(hint, gitRef string) string {
	if hint == "" {
		return ""
	}
	short := shortRef(gitRef)
	hint = strings.ReplaceAll(hint, "{prev_sha}", gitRef)
	hint = strings.ReplaceAll(hint, "{prev_sha_short}", short)
	return hint
}
