// cmd/regis/cmd/schema.go
package cmd

import (
	"os"

	regis "git.disroot.org/jmy/regis"
	"github.com/spf13/cobra"
)

func newSchemaCommand(gf *GlobalFlags) *cobra.Command {
	var output string

	c := &cobra.Command{
		Use:   "schema",
		Short: "print the regis.yml schema reference",
		Long: `regis schema prints a quick reference for all regis.yml fields,
nature types, and an annotated example.

By default output goes to stdout. Use --output to write to a file.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if output == "" {
				_, err := cmd.OutOrStdout().Write(regis.SchemaDoc)
				return err
			}
			return os.WriteFile(output, regis.SchemaDoc, 0644)
		},
	}
	c.Flags().StringVarP(&output, "output", "o", "", "write to file instead of stdout")
	return c
}
