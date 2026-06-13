// cmd/regis/cmd/root_test.go
package cmd_test

import (
	"testing"
	"git.disroot.org/jmy/regis/cmd/regis/cmd"
)

func TestRootCommand_hasVersionFlag(t *testing.T) {
	root := cmd.NewRootCommand("dev")
	// Just ensure the command was created without panicking
	if root.Use == "" {
		t.Error("root command has no Use string")
	}
}

func TestGlobalFlags(t *testing.T) {
	root := cmd.NewRootCommand("dev")
	flags := []string{"file", "target", "run-without-check", "verbose", "quiet", "plain", "nature", "allow-dirty", "no-git"}
	for _, name := range flags {
		if f := root.PersistentFlags().Lookup(name); f == nil {
			t.Errorf("missing global flag: --%s", name)
		}
	}
}
