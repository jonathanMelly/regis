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
				dir := tgt.Dir
				// ~ is not expanded inside single quotes; replace with $HOME so the
				// remote shell expands it correctly inside double quotes.
				if dir == "~" {
					dir = "$HOME"
				} else if strings.HasPrefix(dir, "~/") {
					dir = "$HOME/" + dir[2:]
				}
				dir = strings.ReplaceAll(dir, `\`, `\\`)
				dir = strings.ReplaceAll(dir, `"`, `\"`)
				sshArgs = append(sshArgs, "-t", fmt.Sprintf("%s@%s", tgt.User, tgt.Host),
					fmt.Sprintf(`cd "%s" 2>/dev/null; exec $SHELL -l`, dir))
			} else {
				sshArgs = append(sshArgs, fmt.Sprintf("%s@%s", tgt.User, tgt.Host))
			}
			sh := exec.Command("ssh", sshArgs...)
			sh.Stdin = os.Stdin
			sh.Stdout = os.Stdout
			sh.Stderr = os.Stderr
			sh.Env = remapTERM(os.Environ())
			return sh.Run()
		},
	}
}

// remapTERM replaces non-standard TERM values (ghostty, kitty, wezterm, …) with
// xterm-256color so the remote host doesn't need a matching terminfo entry.
// Standard values (xterm*, screen*, tmux*, vt*, linux, ansi) are left as-is.
func remapTERM(env []string) []string {
	out := make([]string, len(env))
	copy(out, env)
	for i, e := range out {
		if !strings.HasPrefix(e, "TERM=") {
			continue
		}
		val := e[5:]
		if isSafeTERM(val) {
			break
		}
		out[i] = "TERM=xterm-256color"
		break
	}
	return out
}

// isSafeTERM returns true for TERM values that are present in virtually every
// Unix terminfo database. "xterm-ghostty", "xterm-kitty" etc. start with "xterm"
// but are NOT universally available, so we use an explicit allowlist.
var safeTERMs = map[string]bool{
	"xterm": true, "xterm-256color": true, "xterm-color": true, "xterm-16color": true,
	"screen": true, "screen-256color": true,
	"tmux": true, "tmux-256color": true,
	"vt100": true, "vt220": true, "vt320": true,
	"linux": true, "ansi": true, "dumb": true,
}

func isSafeTERM(term string) bool { return safeTERMs[term] }

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
