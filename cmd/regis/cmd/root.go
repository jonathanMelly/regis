// cmd/regis/cmd/root.go
package cmd

import (
	"github.com/spf13/cobra"
)

// GlobalFlags holds values for flags shared across all commands.
type GlobalFlags struct {
	File           string
	Target         string
	DryRun         bool   // simulate full deploy flow without making changes
	Yes            bool   // skip inline confirmation prompts (internal alias for RunWithoutCheck)
	RunWithoutCheck bool  // deploy without showing rdiff preview first (CI/automation)
	Verbose        bool
	Quiet          bool
	Plain          bool
	Nature         string // comma-separated: binary,config,secret,action
	Debug          bool
	AllowDirty     bool // allow rdiff and deploy with uncommitted changes (git_ref approximate)
	NoGit          bool // allow rdiff and deploy without a git repository (no git_ref recorded)
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
	pf.BoolVarP(&gf.DryRun, "dry-run", "n", false, "simulate the full deploy flow without making changes (shows rdiff, then simulates deploy)")
	pf.BoolVar(&gf.RunWithoutCheck, "run-without-check", false, "deploy without showing rdiff preview first (for CI/automation — no shortcut to prevent accidental use)")
	pf.BoolVarP(&gf.Verbose, "verbose", "v", false, "show unchanged cues + full command output")
	pf.BoolVarP(&gf.Quiet, "quiet", "q", false, "errors and summary only")
	pf.BoolVar(&gf.Plain, "plain", false, "force plain output (level 1)")
	pf.StringVarP(&gf.Nature, "nature", "N", "", "filter cues by nature: binary,config,secret,action")
	pf.BoolVar(&gf.Debug, "debug", false, "log every SSH command to stderr")
	pf.BoolVar(&gf.AllowDirty, "allow-dirty", false, "allow rdiff and deploy with uncommitted changes (warns in state, git_ref is approximate)")
	pf.BoolVar(&gf.NoGit, "no-git", false, "allow rdiff and deploy without a git repository (no git_ref recorded in state)")

	// Add subcommands
	root.AddCommand(
		newRunCommand(&gf),
		newRdiffCommand(&gf),
		newStateCommand(&gf),
		newFetchCommand(&gf),
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
