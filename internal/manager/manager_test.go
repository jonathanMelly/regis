// internal/manager/manager_test.go
package manager_test

import (
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
