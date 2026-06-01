// cmd/regis/cmd/service.go
package cmd

import (
	"fmt"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/manager"
	regssh "git.disroot.org/jmy/regis/internal/ssh"
	"github.com/spf13/cobra"
)

func newServiceCommand(gf *GlobalFlags) *cobra.Command {
	svc := &cobra.Command{
		Use:   "service",
		Short: "manage services on the target (start, stop, restart, reload, enable, disable, logs)",
	}

	for _, action := range []string{"start", "stop", "restart", "reload", "enable", "disable"} {
		action := action // capture loop variable
		svc.AddCommand(&cobra.Command{
			Use:   action + " <name>",
			Short: action + " a service",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runServiceAction(gf, args[0], action)
			},
		})
	}

	svc.AddCommand(&cobra.Command{
		Use:   "logs <name>",
		Short: "tail -f the service log on target",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServiceLogs(gf, args[0])
		},
	})

	return svc
}

func runServiceAction(gf *GlobalFlags, svcName, action string) error {
	cfg, err := config.Load(gf.File)
	if err != nil {
		return err
	}
	tgt, svcCue, err := resolveServiceTarget(cfg, gf.Target, svcName)
	if err != nil {
		return err
	}
	conn, err := regssh.Dial(tgt)
	if err != nil {
		return err
	}
	defer conn.Close()

	cmds := manager.ExpandCommands(svcCue, tgt)
	shellCmd, ok := cmds[action]
	if !ok {
		return fmt.Errorf("service %q has no %s command", svcName, action)
	}

	stdout, stderr, code, err := conn.Run(shellCmd)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("exit %d: %s", code, stderr)
	}
	if stdout != "" {
		fmt.Print(stdout)
	}
	fmt.Printf("service %s %s: ok\n", svcName, action)
	return nil
}

func runServiceLogs(gf *GlobalFlags, svcName string) error {
	cfg, err := config.Load(gf.File)
	if err != nil {
		return err
	}
	tgt, svcCue, err := resolveServiceTarget(cfg, gf.Target, svcName)
	if err != nil {
		return err
	}
	conn, err := regssh.Dial(tgt)
	if err != nil {
		return err
	}
	defer conn.Close()

	logPath := fmt.Sprintf("%s/%s.log", tgt.Dir, svcCue.Binary)
	stdout, _, _, err := conn.Run(fmt.Sprintf("tail -f %s", logPath))
	if err != nil {
		return err
	}
	fmt.Print(stdout)
	return nil
}

func resolveServiceTarget(cfg *config.Config, targetSel, svcName string) (config.Target, config.CueRef, error) {
	var tgt config.Target
	var tgtNames []string
	for _, t := range cfg.Targets {
		tgtNames = append(tgtNames, t.Name)
	}
	selected := SelectTargets(tgtNames, targetSel)
	if len(selected) == 0 {
		return tgt, config.CueRef{}, fmt.Errorf("no targets matched")
	}
	for _, t := range cfg.Targets {
		if t.Name == selected[0] {
			tgt = t
			break
		}
	}
	for _, sc := range cfg.Scenarios {
		for _, cr := range sc.Cues {
			if cr.ScenarioRef != "" {
				continue
			}
			if cr.Nature == "service" && cr.Name == svcName {
				return tgt, cr, nil
			}
		}
	}
	return tgt, config.CueRef{}, fmt.Errorf("service %q not defined in any scenario", svcName)
}
