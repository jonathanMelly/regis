// cmd/regis/cmd/rdiff_test.go
package cmd_test

import (
	"testing"
	"git.disroot.org/jmy/regis/cmd/regis/cmd"
)

func TestRdiffCommand_registered(t *testing.T) {
	root := cmd.NewRootCommand("dev")
	for _, c := range root.Commands() {
		if c.Name() == "rdiff" || c.Name() == "status" {
			return
		}
	}
	t.Error("rdiff command not registered")
}
