// internal/config/types_test.go
package config_test

import (
	"testing"
	"git.disroot.org/jmy/regis/internal/config"
)

func TestConfigZeroValue(t *testing.T) {
	var c config.Config
	if c.Run.Mode != "" {
		t.Errorf("want empty Run.Mode, got %q", c.Run.Mode)
	}
}

func TestTargetDefaults(t *testing.T) {
	tgt := config.Target{Name: "prod", Host: "h", User: "u", Dir: "/opt"}
	if tgt.Port != "" {
		t.Errorf("want empty port (will default to 22), got %q", tgt.Port)
	}
}
