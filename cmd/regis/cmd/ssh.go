// cmd/regis/cmd/ssh.go
package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"git.disroot.org/jmy/regis/internal/config"
	regssh "git.disroot.org/jmy/regis/internal/ssh"
	"github.com/spf13/cobra"
)

func newSSHCommand(gf *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "ssh",
		Short: "open an interactive SSH session to the target",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(gf.File)
			if err != nil {
				return err
			}
			var tgtNames []string
			for _, t := range cfg.Targets {
				tgtNames = append(tgtNames, t.Name)
			}
			selected := SelectTargets(tgtNames, gf.Target)
			if len(selected) == 0 {
				return fmt.Errorf("no targets matched")
			}
			var tgt config.Target
			for i := range cfg.Targets {
				if cfg.Targets[i].Name == selected[0] {
					_ = config.InterpolateForTarget(cfg, &cfg.Targets[i])
					tgt = cfg.Targets[i]
					break
				}
			}
			port := 22
			if tgt.Port != "" {
				if n, err := strconv.Atoi(tgt.Port); err == nil {
					port = n
				}
			}
			sshArgs := []string{"-p", fmt.Sprintf("%d", port)}
			if tgt.Dir != "" {
				dir := strings.ReplaceAll(tgt.Dir, "'", `'\''`)
				sshArgs = append(sshArgs, "-t", fmt.Sprintf("%s@%s", tgt.User, tgt.Host),
					fmt.Sprintf("cd '%s' 2>/dev/null; exec $SHELL -l", dir))
			} else {
				sshArgs = append(sshArgs, fmt.Sprintf("%s@%s", tgt.User, tgt.Host))
			}
			sh := exec.Command("ssh", sshArgs...)
			sh.Stdin = os.Stdin
			sh.Stdout = os.Stdout
			sh.Stderr = os.Stderr
			return sh.Run()
		},
	}
}

func newExecCommand(gf *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   `exec "<command>"`,
		Short: "run a raw SSH command on the target (escape hatch)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConn(gf, func(conn *regssh.Conn, _ config.Target, _ *config.Config) error {
				stdout, stderr, code, err := conn.Run(args[0])
				if err != nil {
					return err
				}
				fmt.Print(stdout)
				if stderr != "" {
					fmt.Fprint(os.Stderr, stderr)
				}
				if code != 0 {
					os.Exit(code)
				}
				return nil
			})
		},
	}
}
