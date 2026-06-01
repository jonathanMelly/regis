// cmd/regis/cmd/ai.go
package cmd

import (
	"os"

	regis "git.disroot.org/jmy/regis"
	"github.com/spf13/cobra"
)

func newAICommand(gf *GlobalFlags) *cobra.Command {
	var stdout bool
	var output string

	c := &cobra.Command{
		Use:   "ai",
		Short: "output embedded regis schema context for AI-assisted regis.yml authoring",
		Long: `regis ai outputs a condensed schema reference that AI agents can read
to understand regis and generate regis.yml for any project.

Workflow:
  regis ai > regis-ai.md
  # Give regis-ai.md + your project files to an AI assistant`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if stdout {
				_, err := cmd.OutOrStdout().Write(regis.AIContext)
				return err
			}
			outPath := output
			if outPath == "" {
				outPath = "regis-ai.md"
			}
			return os.WriteFile(outPath, regis.AIContext, 0644)
		},
	}
	c.Flags().BoolVar(&stdout, "stdout", false, "write to stdout instead of a file")
	c.Flags().StringVarP(&output, "output", "o", "", "output file path (default: regis-ai.md)")
	return c
}
