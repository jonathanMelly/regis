// cmd/regis/cmd/schema.go
package cmd

import (
	"fmt"
	"os"

	regis "git.disroot.org/jmy/regis"
	"github.com/spf13/cobra"
)

func newSchemaCommand(gf *GlobalFlags) *cobra.Command {
	var stdout bool
	var output string

	c := &cobra.Command{
		Use:   "schema",
		Short: "write the annotated regis.yml schema to regis-schema.yml",
		Long: `regis schema writes the annotated regis.yml schema to regis-schema.yml.

All fields are shown with types, defaults, and inline comments.
Use --stdout to print to stdout, or --output to write to a custom path.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if stdout {
				_, err := cmd.OutOrStdout().Write(regis.SchemaDoc)
				return err
			}
			outPath := output
			if outPath == "" {
				outPath = "regis-schema.yml"
			}
			if err := os.WriteFile(outPath, regis.SchemaDoc, 0644); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s written — have a look with your favourite editor\n", outPath)
			return nil
		},
	}
	c.Flags().BoolVar(&stdout, "stdout", false, "write to stdout instead of a file")
	c.Flags().StringVarP(&output, "output", "o", "", "output file path (default: regis-schema.yml)")
	return c
}
