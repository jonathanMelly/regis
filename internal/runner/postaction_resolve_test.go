// internal/runner/postaction_resolve_test.go
package runner

import (
	"testing"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/cue"
)

func cfg1svc(name, mgr string, sudo bool, commands map[string]string) *config.Config {
	return &config.Config{
		Scenarios: map[string]config.Scenario{
			"svc": {Cues: []config.CueRef{
				{Name: name, Nature: "service", Manager: mgr, Sudo: sudo, Commands: commands},
			}},
		},
	}
}

func TestResolvePostAction_rawCommand(t *testing.T) {
	pa := cue.PostAction{Cmd: "nginx -t && nginx -s reload", Sudo: true}
	cmd, sudo := resolvePostAction(pa, &config.Config{}, config.Target{})
	if cmd != "nginx -t && nginx -s reload" || !sudo {
		t.Errorf("unexpected: cmd=%q sudo=%v", cmd, sudo)
	}
}

func TestResolvePostAction_restartSystemd(t *testing.T) {
	cfg := cfg1svc("saver", "systemd", false, nil)
	pa := cue.PostAction{Cmd: "restart:saver"}
	cmd, sudo := resolvePostAction(pa, cfg, config.Target{})
	if cmd != "systemctl restart saver" {
		t.Errorf("want systemctl restart saver, got %q", cmd)
	}
	if sudo {
		t.Error("sudo should be false (service.Sudo=false, pa.Sudo=false)")
	}
}

func TestResolvePostAction_reloadCustom(t *testing.T) {
	cfg := cfg1svc("nginx-front", "systemd", true, map[string]string{
		"reload": "nginx -t && nginx -s reload",
	})
	pa := cue.PostAction{Cmd: "reload:nginx-front"}
	cmd, sudo := resolvePostAction(pa, cfg, config.Target{})
	if cmd != "nginx -t && nginx -s reload" {
		t.Errorf("want custom reload command, got %q", cmd)
	}
	if !sudo {
		t.Error("sudo should be true (service.Sudo=true)")
	}
}

func TestResolvePostAction_sudoFromPA(t *testing.T) {
	// pa.Sudo=true should win even if service.Sudo=false
	cfg := cfg1svc("saver", "systemd", false, nil)
	pa := cue.PostAction{Cmd: "restart:saver", Sudo: true}
	_, sudo := resolvePostAction(pa, cfg, config.Target{})
	if !sudo {
		t.Error("sudo should be true when pa.Sudo=true")
	}
}

func TestResolvePostAction_unknownService(t *testing.T) {
	// No matching service → raw command returned unchanged.
	pa := cue.PostAction{Cmd: "restart:unknown", Sudo: false}
	cmd, sudo := resolvePostAction(pa, &config.Config{}, config.Target{})
	if cmd != "restart:unknown" || sudo {
		t.Errorf("unknown service: want raw cmd back, got %q sudo=%v", cmd, sudo)
	}
}

func TestResolvePostAction_deploySystemd(t *testing.T) {
	cfg := cfg1svc("mailway", "systemd", true, nil)
	pa := cue.PostAction{Cmd: "deploy:mailway"}
	cmd, sudo := resolvePostAction(pa, cfg, config.Target{})
	if cmd != "systemctl daemon-reload && systemctl enable mailway" {
		t.Errorf("want systemd deploy command, got %q", cmd)
	}
	if !sudo {
		t.Error("sudo should be true (service.Sudo=true)")
	}
}
