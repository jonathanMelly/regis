// cmd/regis/main.go
package main

import (
	"fmt"
	"os"
	"git.disroot.org/jmy/regis/cmd/regis/cmd"
)

var version = "dev"

func main() {
	if err := cmd.NewRootCommand(version).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
