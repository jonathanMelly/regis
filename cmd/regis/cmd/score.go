// cmd/regis/cmd/score.go
package cmd

import (
	"fmt"
	"os"
	"strings"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/score"
	"github.com/spf13/cobra"
)

func newScoreCommand(gf *GlobalFlags) *cobra.Command {
	var mermaid bool
	var format string
	var compact bool
	var sortMode string
	var maxDepth int

	c := &cobra.Command{
		Use:     "score [scenario,...]",
		Aliases: []string{"show"},
		Short:   "show scenario/cue structure and dependencies",
		Long: `regis score displays the deployment structure.

  regis score                   all scenarios, tree format
  regis score saver,dashboard   filter to named scenarios
  regis score --format flow     pipeline stages + cue detail
  regis score --compact         one line per scenario

Formats:
  tree    (default) scenario cards with tree branches; ↑ dep lines
  flow    pipeline stages first, then per-scenario cue detail`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(gf.File)
			if err != nil {
				return err
			}

			// Parse filter from args: "saver,dashboard" or separate args
			var filter []string
			for _, a := range args {
				for _, s := range strings.Split(a, ",") {
					if s = strings.TrimSpace(s); s != "" {
						filter = append(filter, s)
					}
				}
			}

			if mermaid {
				content := score.RenderMermaidFile(cfg)
				outPath := ".regis/score.md"
				os.MkdirAll(".regis", 0755)
				if err := os.WriteFile(outPath, []byte(content), 0644); err != nil {
					return fmt.Errorf("write %s: %w", outPath, err)
				}
				fmt.Printf("Mermaid diagram written to %s\n", outPath)
				return nil
			}

			switch {
			case compact:
				fmt.Print(score.RenderCompact(cfg, filter, sortMode))
			case format == "flow":
				fmt.Print(score.RenderFlow(cfg, filter, sortMode))
			default:
				fmt.Print(score.RenderTree(cfg, filter, sortMode, maxDepth))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&mermaid, "mermaid", false, "write Mermaid diagram to .regis/score.md")
	c.Flags().StringVar(&format, "format", "tree", "output format: tree | flow")
	c.Flags().BoolVar(&compact, "compact", false, "one line per scenario with cue list")
	c.Flags().StringVar(&sortMode, "sort", "yaml", "scenario sort order: yaml | alpha")
	c.Flags().IntVar(&maxDepth, "max-depth", 5, "max depth for expanding scenario refs (0 = no expansion)")
	return c
}
