// cmd/regis/cmd/run.go
package cmd

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
	"git.disroot.org/jmy/regis/internal/output"
	"git.disroot.org/jmy/regis/internal/runner"
	"git.disroot.org/jmy/regis/internal/score"
	regssh "git.disroot.org/jmy/regis/internal/ssh"
)

var reservedNames = map[string]bool{
	"config": true, "init": true, "score": true, "show": true,
	"fetch": true, "rdiff": true, "status": true, "release": true,
	"releases": true, "service": true, "ssh": true, "exec": true,
	"env": true, "ai": true,
}

// populateRemoteFiles runs a single find on the target and stores the file set
// in ctx so executors can skip download/hash round-trips for absent files.
func populateRemoteFiles(ctx context.Context, conn cue.SSHConn, dir string) context.Context {
	if conn == nil {
		return ctx
	}
	if stdout, _, _, err := conn.Run(fmt.Sprintf("find %s -type f 2>/dev/null", dir)); err == nil {
		ctx = cue.WithRemoteFiles(ctx, strings.Split(stdout, "\n"))
	}
	return ctx
}

// IsReservedScenarioName reports whether name is a built-in command name.
func IsReservedScenarioName(name string) bool {
	return reservedNames[name]
}

// ParseRunArgs splits a comma-separated run argument into scenario names and
// scoped cue filters. Tokens of the form "scenario:cue" add the scenario to
// the name list and record the cue under ScopedCues; plain tokens are scenario
// names. Duplicate scenario names are collapsed. An error is returned for
// malformed ":" tokens where either side is empty.
func ParseRunArgs(arg string) (scenarioNames []string, scopedCues map[string][]string, err error) {
	seen := make(map[string]bool)
	for _, tok := range strings.Split(arg, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if idx := strings.IndexByte(tok, ':'); idx >= 0 {
			scName, cueName := tok[:idx], tok[idx+1:]
			if scName == "" || cueName == "" {
				return nil, nil, fmt.Errorf("invalid filter %q — use scenario:cue format", tok)
			}
			if scopedCues == nil {
				scopedCues = make(map[string][]string)
			}
			scopedCues[scName] = append(scopedCues[scName], cueName)
			if !seen[scName] {
				scenarioNames = append(scenarioNames, scName)
				seen[scName] = true
			}
		} else {
			if !seen[tok] {
				scenarioNames = append(scenarioNames, tok)
				seen[tok] = true
			}
		}
	}
	return
}

// ParseNatureFilter splits a comma-separated nature filter string.
func ParseNatureFilter(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// SelectTargets returns target names matched by selector.
// Selector: "all" = all targets, "name1,name2" = list, "prod*" = glob, "" = first.
func SelectTargets(targetNames []string, selector string) []string {
	if selector == "" || selector == "first" {
		if len(targetNames) > 0 {
			return []string{targetNames[0]}
		}
		return nil
	}
	if selector == "all" {
		return targetNames
	}
	// Comma-separated list
	if strings.Contains(selector, ",") {
		parts := strings.Split(selector, ",")
		var out []string
		for _, p := range parts {
			p = strings.TrimSpace(p)
			for _, t := range targetNames {
				if t == p {
					out = append(out, t)
				}
			}
		}
		return out
	}
	// Glob
	var out []string
	for _, t := range targetNames {
		if matched, _ := filepath.Match(selector, t); matched {
			out = append(out, t)
		}
	}
	return out
}

func newRunCommand(gf *GlobalFlags) *cobra.Command {
	var secretOnly bool
	var pruneReleases bool
	var fresh bool
	var forceManifest bool

	c := &cobra.Command{
		Use:   "run [scenario[,scenario...]]",
		Short: "run one or more scenarios (omit to run all; also: regis <scenario> directly)",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(gf.File)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			// No explicit scenarios → run all in public-group-first order (same as score).
			var scenarioNames []string
			var scopedCues map[string][]string
			if len(args) == 0 {
				scenarioNames = score.SortedScenarioNames(cfg, "yaml")
			} else {
				var parseErr error
				scenarioNames, scopedCues, parseErr = ParseRunArgs(args[0])
				if parseErr != nil {
					return parseErr
				}
				for _, name := range scenarioNames {
					if strings.HasPrefix(name, ":") {
						continue // built-in override — handled by cobra dispatch
					}
					if IsReservedScenarioName(name) {
						fmt.Fprintf(os.Stderr, "note: %q resolved to scenario — use :%s for the built-in\n", name, name)
					}
				}
			}

			var targetNames []string
			for _, t := range cfg.Targets {
				targetNames = append(targetNames, t.Name)
			}
			selected := SelectTargets(targetNames, gf.Target)
			if len(selected) == 0 {
				return fmt.Errorf("no targets matched selector %q", gf.Target)
			}

			nature := gf.Nature
			if secretOnly {
				nature = "secret"
			}

			level := output.DetectLevel()
			if gf.Plain {
				level = output.Level1
			}
			for _, tgtName := range selected {
				var tgt config.Target
				for i := range cfg.Targets {
					if cfg.Targets[i].Name == tgtName {
						_ = config.InterpolateForTarget(cfg, &cfg.Targets[i])
						tgt = cfg.Targets[i]
						break
					}
				}

				output.PrintOpeningQuote(level)
				if gf.Debug {
					port := "22"
					if tgt.Port != "" {
						port = tgt.Port
					}
					fmt.Fprintf(os.Stderr, "[debug] dialing %s@%s:%s\n", tgt.User, tgt.Host, port)
				}
				spinner := output.NewSpinner(level, fmt.Sprintf("connecting to %s...", tgtName))
				spinner.Start()

				rawConn, dialErr := regssh.Dial(tgt)
				if gf.Debug && dialErr != nil {
					fmt.Fprintf(os.Stderr, "[debug] dial error: %v\n", dialErr)
				}
				var conn cue.SSHConn
				if rawConn != nil {
					if expanded, err := rawConn.ExpandHome(tgt.Dir); err != nil {
						fmt.Fprintf(os.Stderr, "FAILED  %s: %v\n", tgtName, err)
						spinner.Stop()
						os.Exit(1)
					} else {
						tgt.Dir = expanded
					}
					conn = WrapDebug(rawConn, gf.Debug)
					spinner.Update(fmt.Sprintf("checking %s...", tgtName))
				}

				// --fresh: backup then wipe target dir before deploying.
				if fresh {
					if rawConn == nil {
						spinner.Stop()
						return fmt.Errorf("--fresh requires a live SSH connection")
					}
					cleanDir := path.Clean(tgt.Dir)
					ts := time.Now().UTC().Format("20060102-150405")
					backupPath := path.Join(path.Dir(cleanDir), path.Base(cleanDir)+"-bak-"+ts+".tar.gz")

					spinner.Stop()
					if !gf.Yes {
						fmt.Printf("\nfresh deploy on %s: all files in %s will be deleted\n", tgtName, cleanDir)
						fmt.Printf("backup → %s\n", backupPath)
						fmt.Printf("proceed? [y/N] ")
						var ans string
						fmt.Scan(&ans)
						if strings.ToLower(strings.TrimSpace(ans)) != "y" {
							fmt.Println("aborted")
							rawConn.Close()
							continue
						}
					}

					// Backup.
					backupCmd := fmt.Sprintf("tar czf %s -C %s %s",
						backupPath, path.Dir(cleanDir), path.Base(cleanDir))
					var bErr string
					var bCode int
					if tgt.Sudo {
						_, bErr, bCode, _ = rawConn.RunSudo(backupCmd)
					} else {
						_, bErr, bCode, _ = rawConn.Run(backupCmd)
					}
					if bCode != 0 {
						fmt.Fprintf(os.Stderr, "warn: backup failed (exit %d): %s\n", bCode, bErr)
						if !gf.Yes {
							fmt.Printf("backup failed — continue with wipe anyway? [y/N] ")
							var ans string
							fmt.Scan(&ans)
							if strings.ToLower(strings.TrimSpace(ans)) != "y" {
								rawConn.Close()
								continue
							}
						}
					} else {
						fmt.Printf("backup  %s\n", backupPath)
					}

					// Wipe — explicitly exclude backup path as safety net against trailing-slash edge cases.
					wipeCmd := fmt.Sprintf("find %s -mindepth 1 ! -path %s -delete", cleanDir, backupPath)
					var wErr string
					var wCode int
					if tgt.Sudo {
						_, wErr, wCode, _ = rawConn.RunSudo(wipeCmd)
					} else {
						_, wErr, wCode, _ = rawConn.Run(wipeCmd)
					}
					if wCode != 0 {
						rawConn.Close()
						return fmt.Errorf("wipe %s failed (exit %d): %s", cleanDir, wCode, wErr)
					}
					fmt.Printf("wiped   %s\n", cleanDir)

					spinner = output.NewSpinner(level, fmt.Sprintf("deploying %s...", tgtName))
					spinner.Start()
				}

				env, _ := config.BuildEnvForTarget(cfg, &tgt)
				dispatch := runner.Dispatch{
					BulkConn: conn,
					Binary:   cue.NewBinaryExecutor(conn),
					Config:   cue.NewConfigExecutor(conn, env),
					Secret:   cue.NewSecretExecutor(conn),
					Action:   cue.NewActionExecutor(conn),
					Generate: cue.NewGenerateExecutor(),
					Render:   cue.NewRenderExecutor(conn),
					Pack:     cue.NewPackExecutor(conn).WithReleaseDir(cfg.Release.Dir, gf.Yes),
					Service:  cue.NewServiceExecutor(conn, env),
				}

				ctx := populateRemoteFiles(context.Background(), conn, tgt.Dir)
				ctx = cue.WithLocalDir(ctx, cfg.BaseDir)
				if gf.Debug {
					ctx = cue.WithDebugWriter(ctx, os.Stderr)
				}
				ctx = cue.WithCueProgress(ctx, func(checked, total int) {
					spinner.Update(fmt.Sprintf("checking %s... %d/%d", tgtName, checked, total))
				})
				ctx = cue.WithPreStep(ctx, func(scenario, cueName, desc string) {
					label := desc
					if label == "" {
						label = scenario
					}
					spinner.Update(fmt.Sprintf("deploying %s... %s / %s", tgtName, label, cueName))
				})
				ctx = cue.WithFileProgress(ctx, func(cueName string, scanned, total int) {
					spinner.Update(fmt.Sprintf("deploying %s... %s  %d/%d", tgtName, cueName, scanned, total))
				})
				spinner.Update(fmt.Sprintf("deploying %s...", tgtName))

				onResult := func(r cue.Result) {
					if level < output.Level2 {
						fmt.Printf("[%s] %-24s %s\n", tgtName, r.CueName, r.Status.Applied())
					}
				}

				result, err := runner.Run(ctx, cfg, scenarioNames, tgt,
					runner.Options{
					DryRun:        gf.DryRun,
					SkipConfirm:   gf.Yes,
					NatureFilter:  ParseNatureFilter(nature),
					PruneReleases: pruneReleases,
					ForceManifest: forceManifest,
					ScopedCues:    scopedCues,
				},
					dispatch, onResult)
				spinner.Stop()
				if rawConn != nil {
					rawConn.Close()
				}
				if err != nil {
					fmt.Fprintf(os.Stderr, "FAILED  %s: %v\n", tgtName, err)
					os.Exit(1)
				}
				if result != nil {
					if level >= output.Level2 {
						fmt.Print(output.RenderTable(result.Results, tgtName, result.Elapsed, true, level, gf.Verbose))
					} else {
						fmt.Print(output.RenderPlain(result.Results, tgtName, result.Elapsed, true))
					}
				}
			}
			return nil
		},
	}

	c.Flags().BoolVarP(&secretOnly, "secrets", "s", false, "shorthand for --nature secret")
	c.Flags().BoolVar(&pruneReleases, "prune-releases", false, "prune old releases (remote + local) after deploy")
	c.Flags().BoolVar(&fresh, "fresh", false, "backup then wipe target dir before deploying (prompts for confirmation)")
	c.Flags().BoolVar(&forceManifest, "force-manifest", false, "write release manifest even when nothing changed (useful after manual file sync)")
	return c
}
