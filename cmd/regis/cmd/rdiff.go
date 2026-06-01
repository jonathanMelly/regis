// cmd/regis/cmd/rdiff.go
package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

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

func newRdiffCommand(gf *GlobalFlags) *cobra.Command {
	var noDiff bool
	cmd := &cobra.Command{
		Use:     "rdiff",
		Aliases: []string{"status"},
		Short:   "show current sync state — are local cues in sync with remote?",
		Long: `regis rdiff compares local cues against remote state.
Analogous to 'git status': shows what would change if you ran regis now.
Secret cues show key presence only — values never shown.`,
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

			level := output.DetectLevel()
			if gf.Plain {
				level = output.Level1
			}
			showDiff := !noDiff

			for _, tgtName := range selected {
				var tgt config.Target
				for _, t := range cfg.Targets {
					if t.Name == tgtName {
						tgt = t
					}
				}

				spinner := output.NewSpinner(level, fmt.Sprintf("connecting to %sâ¦", tgtName))
				spinner.Start()

				rawConn, _ := regssh.Dial(tgt)
				var conn cue.SSHConn
				if rawConn != nil {
					conn = WrapDebug(rawConn, gf.Debug)
					spinner.Update(fmt.Sprintf("checking %sâ¦", tgtName))
				}

				// Download and parse the release manifest for drift detection.
				ctx := context.Background()
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
								Checksums:  m.Checksums,
							})
						}
					}
				}

				env, _ := config.BuildEnvForTarget(cfg, &tgt)
				dispatch := runner.Dispatch{
					Binary:   cue.NewBinaryExecutor(conn),
					Config:   cue.NewConfigExecutor(conn, env),
					Secret:   cue.NewSecretExecutor(conn),
					Action:   cue.NewActionExecutor(conn),
					Generate: cue.NewGenerateExecutor(),
					Render:   cue.NewRenderExecutor(conn),
					Pack:     cue.NewPackExecutor(conn),
					Service:  cue.NewServiceExecutor(conn, env),
				}

				if level < output.Level2 {
					fmt.Fprintf(os.Stderr, "checking the marks — %s…\n", tgtName)
				}

				// Public (uppercase-initial) scenarios first, then lowercase building
				// blocks, both in YAML declaration order — same ordering as score.
				allScenarios := score.SortedScenarioNames(cfg, "yaml")

				result, err := runner.Run(ctx, cfg, allScenarios, tgt,
					runner.Options{DryRun: true},
					dispatch,
					func(r cue.Result) {
						if level < output.Level2 {
							fmt.Printf("%-24s %s\n", r.CueName, r.Status.String())
							return
						}
						label := r.ScenarioName
						if r.ScenarioDesc != "" {
							label = r.ScenarioDesc
						}
						msg := fmt.Sprintf("checking %s", tgtName)
						if label != "" {
							msg += " — " + strings.TrimSpace(label) + " / " + r.CueName
						} else {
							msg += " — " + r.CueName
						}
						spinner.Update(msg)
					},
				)
				if rawConn != nil {
					rawConn.Close()
				}
				spinner.Stop()
				if err != nil {
					fmt.Fprintf(os.Stderr, "rdiff %s: %v\n", tgtName, err)
				}
				if result != nil {
					switch {
					case level >= output.Level3:
						if err := tui.RunRdiffTUI(result.Results, tgtName, result.Elapsed, gf.Verbose, level, minfo); err != nil {
							fmt.Fprintf(os.Stderr, "rdiff tui: %v\n", err)
							fmt.Print(output.RenderTree(result.Results, tgtName, result.Elapsed, showDiff, gf.Verbose, level, minfo))
						}
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
	return cmd
}
