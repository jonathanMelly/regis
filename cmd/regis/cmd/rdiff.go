// cmd/regis/cmd/rdiff.go
package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
	"git.disroot.org/jmy/regis/internal/output"
	"git.disroot.org/jmy/regis/internal/runner"
	"git.disroot.org/jmy/regis/internal/score"
	"git.disroot.org/jmy/regis/internal/tui"
)


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

				spinner := output.NewSpinner(level, fmt.Sprintf("connecting to %s...", tgtName))
				spinner.Start()

				rawConn, conn, connErr := connectTarget(gf, &tgt, spinner)
				if connErr != nil {
					fmt.Fprintln(os.Stderr, connErr)
					spinner.Stop()
					os.Exit(1)
				}
				if conn != nil {
					spinner.Update(fmt.Sprintf("checking %s...", tgtName))
				}

				ctx, minfo := buildBaseCtx(gf, conn, tgt, cfg)
				if updateMtime {
					ctx = cue.WithUpdateMtime(ctx)
				}

				spinner.Stop()

				dispatch := buildDispatch(conn, cfg, &tgt, gf, false)
				allScenarios := score.SortedScenarioNames(cfg, "yaml")
				opts := runner.Options{
					DryRun:           true,
					DeduplicateSteps: true,
					ScenarioFilter:   scenarioFilter,
					CueFilter:        cueFilter,
				}

				// Level2: live TUI with browse after check.
				if level >= output.Level2 {
					tuiErr := tui.RunLiveTUI(ctx, tgtName, false, gf.Verbose, level, minfo,
						func(liveCtx context.Context) ([]cue.Result, time.Duration, error) {
							res, runErr := runner.Run(liveCtx, cfg, allScenarios, tgt, opts, dispatch, func(cue.Result) {})
							if res == nil {
								return nil, 0, runErr
							}
							for _, w := range res.SystemWarnings {
								fmt.Fprintf(os.Stderr, "\nwarn: %s\n", w)
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

				// Level1: plain text — run silently, print expand-all tree.
				result, err := runner.Run(ctx, cfg, allScenarios, tgt, opts, dispatch, func(cue.Result) {})
				if rawConn != nil {
					rawConn.Close()
				}
				if err != nil {
					fmt.Fprintf(os.Stderr, "rdiff %s: %v\n", tgtName, err)
				}
				if result != nil {
					fmt.Print(output.RenderTree(result.Results, tgtName, result.Elapsed, showDiff, gf.Verbose, level, minfo))
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&noDiff, "no-diff", false, "suppress text diffs in output")
	cmd.Flags().BoolVar(&updateMtime, "update-mtime", false, "when hash matches, update remote mtime so next rdiff uses the fast mtime/size path")
	return cmd
}
