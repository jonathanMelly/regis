// cmd/regis/cmd/service_test.go
package cmd_test

import (
	"testing"

	"git.disroot.org/jmy/regis/cmd/regis/cmd"
	"github.com/spf13/cobra"
)

func TestServiceCommand_subcommands(t *testing.T) {
	root := cmd.NewRootCommand("dev")
	var serviceCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "service" {
			serviceCmd = c
		}
	}
	if serviceCmd == nil {
		t.Fatal("service command not found")
	}
	want := []string{"start", "stop", "restart", "reload", "enable", "disable", "logs"}
	for _, sub := range want {
		found := false
		for _, c := range serviceCmd.Commands() {
			if c.Name() == sub {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing service subcommand: %s", sub)
		}
	}
}
