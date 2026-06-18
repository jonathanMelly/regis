// internal/manager/systemd.go
package manager

import (
	"fmt"
	"git.disroot.org/jmy/regis/internal/config"
)

func systemdDefaults(cr config.CueRef) map[string]string {
	name := config.ServiceID(cr)
	if name == "" {
		name = cr.Name
	}
	return map[string]string{
		"deploy":  fmt.Sprintf("systemctl daemon-reload && systemctl enable %s", name),
		"enable":  fmt.Sprintf("systemctl enable %s", name),
		"disable": fmt.Sprintf("systemctl disable %s", name),
		"start":   fmt.Sprintf("systemctl start %s", name),
		"stop":    fmt.Sprintf("systemctl stop %s", name),
		"restart": fmt.Sprintf("systemctl restart %s", name),
		"reload":  fmt.Sprintf("systemctl reload %s", name),
		"status":  fmt.Sprintf("systemctl is-active %s", name),
	}
}
