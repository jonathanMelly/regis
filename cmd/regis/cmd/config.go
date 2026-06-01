// cmd/regis/cmd/config.go
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"git.disroot.org/jmy/regis/internal/tui"
)

func newConfigCommand(gf *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:     "config",
		Aliases: []string{"init"},
		Short:   "interactive config wizard — add/edit targets, scenarios, services",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := os.Stat(gf.File)
			if os.IsNotExist(err) {
				fmt.Println("No regis.yml found. Starting creation wizard...")
				model, err := tui.RunWizard()
				if err != nil {
					return fmt.Errorf("wizard: %w", err)
				}
				yaml := model.GenerateYAML()
				if err := os.WriteFile(gf.File, []byte(yaml), 0644); err != nil {
					return fmt.Errorf("write %s: %w", gf.File, err)
				}
				fmt.Printf("\nCreated %s\n", gf.File)
				fmt.Println("Run 'regis score' to visualize your config.")
				return nil
			}
			return tui.RunConfigTUI(gf.File)
		},
	}
}
