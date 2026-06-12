// cmd/regis/cmd/rdiff.go
package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
	"git.disroot.org/jmy/regis/internal/output"
	"git.disroot.org/jmy/regis/internal/runner"
	"git.disroot.org/jmy/regis/internal/score"
	regssh "git.disroot.org/jmy/regis/internal/ssh"
	"git.disroot.org/jmy/regis/internal/tui"
)

// rdiffLiveSymbol returns a single-character status indicator for a live check line.
func rdiffLiveSymbol(r cue.Result) string {
	switch r.Status {
	case cue.StatusEqual:
		return "="
	case cue.StatusChanged:
		return "~"
	case cue.StatusFailed:
		return "!"
	case cue.StatusSkipped:
		if r.Nature == "action" || r.Nature == "service" {
			return "@" // would run in a real deploy; no diff in dry-run
		}
		return "-" // genuinely skipped (if: condition false)
	}
	return "?"
}

func newRdiffCommand(gf *GlobalFlags) *cobra.Command {
	var noDiff bool
	var updateMtime bool
	cmd := &cobra.Command{
		Use:     "rdiff [scenario-or-cue,...]",
		Aliases: []string{"status"},
		Args:    cobra.ArbitraryArgs,
		Short:   "show current sync state — are local cues in sync with remote?",
		Long: `regis rdiff compares local cues against remote state.
Analogous to 'git status': shows what would change if you ran regis now.
Secret cues show key presence only — values never shown.

Optional filter: comma-separated scenario or cue names to check a subset.
  regis rdiff config,app        check only the config and app scenarios
  regis rdiff git\ files        check only the "git files" cue (any scenario)
  regis rdiff config,git\ files mix of scenario and cue name`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(gf.File)
			if err != nil {
				return err
			}

			// Parse optional filter args: tokens that are scenario names become
			// ScenarioFilter; everything else becomes CueFilter.
			var tokens []string
			for _, a := range args {
				for _, t := range strings.Split(a, ",") {
					if t = strings.TrimSpace(t); t != "" {
						tokens = append(tokens, t)
					}
				}
			}
			var scenarioFilter, cueFilter []string
			for _, tok := range tokens {
				if _, ok := cfg.Scenarios[tok]; ok {
					scenarioFilter = append(scenarioFilter, tok)
				} else {
					cueFilter = append(cueFilter, tok)
				}
			}

			var targetNames []string
			for _, t := range cfg.Targets {
				targetNames = append(targetNames, t.Name)
			}
			selected := SelectTargets(targetNames, gf.Target)

			level := output.DetectLevel()
			if gf.Plain {
				level = output.Level1
			}
			showDiff := !noDiff

			for _, tgtName := range selected {
				var tgt config.Target
				for i := range cfg.Targets {
					if cfg.Targets[i].Name == tgtName {
						_ = config.InterpolateForTarget(cfg, &cfg.Targets[i])
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
				spinner := output.NewSpinner(level, fmt.Sprintf("connecting to %s...", tgtName))
				spinner.Start()

				rawConn, dialErr := regssh.Dial(tgt)
				if dialErr != nil {
					fmt.Fprintf(os.Stderr, "warn: SSH connect to %s failed: %v\n", tgtName, dialErr)
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

				// Download and parse the release manifest for drift detection.
				ctx := context.Background()
				if gf.Debug {
					ctx = cue.WithDebugWriter(ctx, os.Stderr)
				}
				var minfo *output.ManifestInfo
				if conn != nil {
					if data, dlErr := conn.Download(tgt.Dir + "/.regis-release"); dlErr == nil {
						var m runner.ReleaseManifest
						if parseErr := yaml.Unmarshal(data, &m); parseErr == nil {
							minfo = &output.ManifestInfo{
								Release:    m.Release,
								DeployedAt: m.DeployedAt,
								DeployedBy: m.DeployedBy,
							}
							ctx = cue.WithManifest(ctx, &cue.Manifest{
								Release:    m.Release,
								DeployedBy: m.DeployedBy,
								Hashes:  m.Hashes,
							})
						}
					}
				}

				ctx = populateRemoteFiles(ctx, conn, tgt.Dir)
				ctx = cue.WithLocalDir(ctx, cfg.BaseDir)
				if updateMtime {
					ctx = cue.WithUpdateMtime(ctx)
				}

				spinner.Stop()

				env, _ := config.BuildEnvForTarget(cfg, &tgt)
				dispatch := runner.Dispatch{
					BulkConn: conn,
					Binary:   cue.NewBinaryExecutor(conn),
					Config:   cue.NewConfigExecutor(conn, env),
					Secret:   cue.NewSecretExecutor(conn),
					Action:   cue.NewActionExecutor(conn),
					Generate: cue.NewGenerateExecutor(),
					Render:   cue.NewRenderExecutor(conn),
					Pack:     cue.NewPackExecutor(conn),
					Service:  cue.NewServiceExecutor(conn, env),
				}
				allScenarios := score.SortedScenarioNames(cfg, "yaml")
				opts := runner.Options{
					DryRun:           true,
					DeduplicateSteps: true,
					ScenarioFilter:   scenarioFilter,
					CueFilter:        cueFilter,
				}

				// Level3: live TUI — checks and browse are one unified phase.
				if level >= output.Level3 {
					tuiErr := tui.RunLiveRdiffTUI(ctx, tgtName, gf.Verbose, level, minfo,
						func(liveCtx context.Context) ([]cue.Result, time.Duration, error) {
							res, runErr := runner.Run(liveCtx, cfg, allScenarios, tgt, opts, dispatch, func(cue.Result) {})
							if res == nil {
								return nil, 0, runErr
							}
							return res.Results, res.Elapsed, runErr
						},
					)
					if rawConn != nil {
						rawConn.Close()
					}
					if tuiErr != nil {
						fmt.Fprintf(os.Stderr, "rdiff tui: %v\n", tuiErr)
					}
					continue
				}

				// Level2: print text lines during check, then static tree.
				if level >= output.Level2 {
					fmt.Printf("checking %s\n", tgtName)
				} else {
					fmt.Fprintf(os.Stderr, "checking the marks — %s…\n", tgtName)
				}

				var (
					liveMu      sync.Mutex
					liveN       int
					liveTotal   int
					skippedCues []string
				)
				ctx = cue.WithPrePhase(ctx, func(steps []cue.StepInfo) {
					if level < output.Level2 {
						return
					}
					liveMu.Lock()
					for _, s := range steps {
						label := s.ScenarioDesc
						if label == "" {
							label = s.ScenarioName
						}
						fmt.Printf("  ·  %s > %s\n", label, s.Name)
					}
					liveMu.Unlock()
				})
				ctx = cue.WithCheckResult(ctx, func(r cue.Result) {
					if level < output.Level2 {
						return
					}
					liveMu.Lock()
					liveN++
					n, tot := liveN, liveTotal
					// Skipped actions are collected and printed as one summary line later.
					if r.Status == cue.StatusSkipped && (r.Nature == "action" || r.Nature == "service") {
						skippedCues = append(skippedCues, r.CueName)
						liveMu.Unlock()
						return
					}
					liveMu.Unlock()
					label := r.ScenarioDesc
					if label == "" {
						label = r.ScenarioName
					}
					sym := rdiffLiveSymbol(r)
					line := fmt.Sprintf("  %s  %s > %s", sym, label, r.CueName)
					if r.FileTotal > 0 {
						line += fmt.Sprintf(" (%d/%d)", r.FileChanged, r.FileTotal)
					}
					if tot > 0 {
						line += fmt.Sprintf("  [cue %d/%d]", n, tot)
					} else {
						line += fmt.Sprintf("  [cue %d]", n)
					}
					fmt.Println(line)
				})
				ctx = cue.WithCueProgress(ctx, func(_, total int) {
					liveMu.Lock()
					liveTotal = total
					liveMu.Unlock()
				})

				result, err := runner.Run(ctx, cfg, allScenarios, tgt, opts, dispatch,
					func(r cue.Result) {
						if level < output.Level2 {
							fmt.Printf("%-24s %s\n", r.CueName, r.Status.String())
						}
					},
				)
				if rawConn != nil {
					rawConn.Close()
				}
				// Print the skipped-action summary after all checks complete.
				if level >= output.Level2 && len(skippedCues) > 0 {
					fmt.Printf("  ·  skipped: %s\n", strings.Join(skippedCues, ", "))
				}
				if err != nil {
					fmt.Fprintf(os.Stderr, "rdiff %s: %v\n", tgtName, err)
				}
				if result != nil {
					switch {
					case level >= output.Level2:
						fmt.Print(output.RenderTree(result.Results, tgtName, result.Elapsed, showDiff, gf.Verbose, level, minfo))
					default:
						fmt.Print(output.RenderPlain(result.Results, tgtName, result.Elapsed, false))
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&noDiff, "no-diff", false, "suppress text diffs in output")
	cmd.Flags().BoolVar(&updateMtime, "update-mtime", false, "when hash matches, update remote mtime so next rdiff uses the fast mtime/size path")
	return cmd
}
