// internal/manager/manager_test.go
package manager_test

import (
	"strings"
	"testing"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/manager"
)

func TestSystemdCommands_defaults(t *testing.T) {
	cr := config.CueRef{
		Name:        "service",
		Nature:      "service",
		Manager:     "systemd",
		ServiceName: "mailway",
	}
	tgt := config.Target{Dir: "/opt/app"}
	cmds := manager.ExpandCommands(cr, tgt)

	cases := map[string]string{
		"start":   "systemctl start mailway",
		"stop":    "systemctl stop mailway",
		"restart": "systemctl restart mailway",
		"reload":  "systemctl reload mailway",
		"enable":  "systemctl enable mailway",
		"disable": "systemctl disable mailway",
		"status":  "systemctl is-active mailway",
		"deploy":  "systemctl daemon-reload && systemctl enable mailway",
	}
	for action, want := range cases {
		if got := cmds[action]; got != want {
			t.Errorf("systemd %s: want %q, got %q", action, want, got)
		}
	}
}

func TestCrontabCommands_defaults(t *testing.T) {
	cr := config.CueRef{
		Name:    "saver",
		Nature:  "service",
		Manager: "crontab",
		Binary:  "saver",
		Health:  "curl -sf http://localhost:8080/health",
	}
	tgt := config.Target{Dir: "/opt/app"}
	cmds := manager.ExpandCommands(cr, tgt)

	if cmds["restart"] == "" {
		t.Error("crontab restart must be defined")
	}
	if cmds["status"] == "" {
		t.Error("crontab status must be defined")
	}
}

// TestCrontabDeploy_idempotentReplace verifies that the deploy command strips existing
// entries for the binary via grep -vF before adding fresh ones, and that watchdog_line
// contains the exact text checkEnabled greps for.
func TestCrontabDeploy_idempotentReplace(t *testing.T) {
	cr := config.CueRef{
		Name:    "saver",
		Nature:  "service",
		Manager: "crontab",
		Binary:  "saver",
		Health:  "curl -sf http://localhost:8080/health > /dev/null",
	}
	tgt := config.Target{Dir: "/opt/custom/saver"}
	cmds := manager.ExpandCommands(cr, tgt)

	deploy := cmds["deploy"]
	watchdogLine := cmds["watchdog_line"]

	// deploy must strip old entries for this binary before adding new ones.
	stripPattern := "/opt/custom/saver/saver"
	if !strings.Contains(deploy, "grep -vF") {
		t.Errorf("deploy must use grep -vF to strip old entries; got:\n%s", deploy)
	}
	if !strings.Contains(deploy, stripPattern) {
		t.Errorf("deploy must strip %q; got:\n%s", stripPattern, deploy)
	}

	// watchdog_line must contain the busy-file guard, the health check, and the binary path.
	for _, want := range []string{
		manager.BusyPath(tgt.Dir),
		"curl -sf http://localhost:8080/health",
		"/opt/custom/saver/saver",
	} {
		if !strings.Contains(watchdogLine, want) {
			t.Errorf("watchdog_line missing %q; got:\n%s", want, watchdogLine)
		}
	}

	// deploy must embed the exact watchdog_line so it can be detected by checkEnabled.
	if !strings.Contains(deploy, watchdogLine) {
		t.Errorf("deploy must contain the exact watchdog_line so checkEnabled can verify it;\ndeploy=%s\nwatchdog_line=%s", deploy, watchdogLine)
	}

	// Simulate replacing an existing crontab that has the old .deploying convention.
	oldCrontab := "@reboot . /opt/custom/saver/.env && nohup /opt/custom/saver/saver >> /opt/custom/saver/saver.log 2>&1 < /dev/null &\n" +
		"*/1 * * * * [ -f /opt/custom/saver/.deploying ] || curl -sf http://localhost:8080/health > /dev/null || (. /opt/custom/saver/.env && nohup /opt/custom/saver/saver >> /opt/custom/saver/saver.log 2>&1 < /dev/null &)\n"
	for _, line := range strings.Split(strings.TrimSpace(oldCrontab), "\n") {
		if !strings.Contains(line, stripPattern) {
			t.Errorf("line not matched by grep -vF %q (would survive deploy): %s", stripPattern, line)
		}
	}
}

func TestCustomManager_requiresCommands(t *testing.T) {
	cr := config.CueRef{
		Name:    "api",
		Nature:  "service",
		Manager: "pm2",
		Commands: map[string]string{
			"start":   "pm2 start api",
			"stop":    "pm2 stop api",
			"restart": "pm2 restart api",
			"status":  "pm2 describe api",
		},
	}
	tgt := config.Target{Dir: "/opt/app"}
	cmds := manager.ExpandCommands(cr, tgt)
	if cmds["restart"] != "pm2 restart api" {
		t.Errorf("custom manager restart: got %q", cmds["restart"])
	}
}

func TestTemplateVars(t *testing.T) {
	cr := config.CueRef{
		Name:        "service",
		Nature:      "service",
		Manager:     "systemd",
		ServiceName: "saver",
	}
	tgt := config.Target{Dir: "/opt/app"}
	cmd := manager.ExpandTemplate("systemctl start {name}", cr, tgt, nil)
	if cmd != "systemctl start saver" {
		t.Errorf("want 'systemctl start saver', got %q", cmd)
	}
}

func TestTemplateVars_superCall(t *testing.T) {
	cr := config.CueRef{
		Name:        "service",
		Nature:      "service",
		Manager:     "systemd",
		ServiceName: "nginx-front",
		Commands: map[string]string{
			"reload": "nginx -t && {reload}",
		},
	}
	tgt := config.Target{}
	cmds := manager.ExpandCommands(cr, tgt)
	want := "nginx -t && systemctl reload nginx-front"
	if cmds["reload"] != want {
		t.Errorf("want %q, got %q", want, cmds["reload"])
	}
}
