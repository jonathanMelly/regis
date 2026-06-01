// cmd/regis/cmd/root.go
package cmd

import (
	"github.com/spf13/cobra"
)

// GlobalFlags holds values for flags shared across all commands.
type GlobalFlags struct {
	File    string
	Target  string
	DryRun  bool
	Yes     bool
	Verbose bool
	Quiet   bool
	Plain   bool
	Nature  string // comma-separated: binary,config,secret,action
	Debug   bool   // log every SSH command to stderr
}

// NewRootCommand builds the root cobra command tree.
func NewRootCommand(version string) *cobra.Command {
	var gf GlobalFlags

	root := &cobra.Command{
		Use:     "regis",
		Version: version,
		Short:   "regis — call the cues. one file, any environment.",
		Long: `regis reads regis.yml and applies your environment state — local tasks
or remote targets over SSH — with optional service management built in.
No daemon. No agent. No cloud dependency.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Global persistent flags (available to all subcommands)
	pf := root.PersistentFlags()
	pf.StringVarP(&gf.File, "file", "f", "regis.yml", "config file")
	pf.StringVar(&gf.Target, "target", "", "target selector (name, comma-list, 'all', glob)")
	pf.BoolVarP(&gf.DryRun, "dry-run", "n", false, "show what would happen without executing")
	pf.BoolVarP(&gf.Yes, "yes", "y", false, "skip confirmation prompts (CI mode)")
	pf.BoolVarP(&gf.Verbose, "verbose", "v", false, "show unchanged cues + full command output")
	pf.BoolVarP(&gf.Quiet, "quiet", "q", false, "errors and summary only")
	pf.BoolVar(&gf.Plain, "plain", false, "force plain output (level 1)")
	pf.StringVarP(&gf.Nature, "nature", "N", "", "filter cues by nature: binary,config,secret,action")
	pf.BoolVar(&gf.Debug, "debug", false, "log every SSH command to stderr")

	// Add subcommands
	root.AddCommand(
		newRunCommand(&gf),
		newRdiffCommand(&gf),
		newFetchCommand(&gf),
		newReleaseCommand(&gf),
		newServiceCommand(&gf),
		newSSHCommand(&gf),
		newExecCommand(&gf),
		newScoreCommand(&gf),
		newConfigCommand(&gf),
		newEnvCommand(&gf),
		newSchemaCommand(&gf),
		newAICommand(&gf),
	)

	root.Args = cobra.ArbitraryArgs
	root.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		return newRunCommand(&gf).RunE(cmd, args)
	}

	return root
}
