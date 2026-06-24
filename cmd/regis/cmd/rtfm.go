// cmd/regis/cmd/rtf.go
package cmd

import (
	"fmt"
	"os"

	regis "git.disroot.org/jmy/regis"
	"github.com/spf13/cobra"
)

func newRTFMCommand(gf *GlobalFlags) *cobra.Command {
	var stdout bool
	var output string

	c := &cobra.Command{
		Use:   "rtfm",
		Short: "output embedded regis reference (schema, CLI, concepts) — useful as AI context",
		Long: `regis rtfm writes the full regis reference to regis-rtfm.md.

The reference covers the complete schema, all nature types, CLI flags, and
key concepts. Useful as AI context when generating or debugging a regis.yml.

Workflow:
  regis rtfm
  # Give regis-rtfm.md + your project files to an AI assistant`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if stdout {
				_, err := cmd.OutOrStdout().Write(regis.RTFContext)
				return err
			}
			outPath := output
			if outPath == "" {
				outPath = "regis-rtfm.md"
			}
			if err := os.WriteFile(outPath, regis.RTFContext, 0644); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s written — have a look with your favourite editor\n", outPath)
			return nil
		},
	}
	c.Flags().BoolVar(&stdout, "stdout", false, "write to stdout instead of a file")
	c.Flags().StringVarP(&output, "output", "o", "", "output file path (default: regis-rtfm.md)")
	return c
}
