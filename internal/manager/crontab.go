// internal/manager/crontab.go
package manager

import (
	"fmt"
	"git.disroot.org/jmy/regis/internal/config"
)

func crontabDefaults(cr config.CueRef, tgt config.Target) map[string]string {
	bin := cr.Binary
	dir := tgt.Dir
	health := cr.Health
	if health == "" {
		health = "true"
	}

	rebootEntry := fmt.Sprintf(
		"@reboot . %s/.env && nohup %s/%s >> %s/%s.log 2>&1 < /dev/null &",
		dir, dir, bin, dir, bin,
	)
	watchdogEntry := fmt.Sprintf(
		`*/1 * * * * [ -f %s/.busy ] || %s || (. %s/.env && nohup %s/%s >> %s/%s.log 2>&1 < /dev/null &)`,
		dir, health, dir, dir, bin, dir, bin,
	)

	return map[string]string{
		"deploy": fmt.Sprintf(
			`(crontab -l 2>/dev/null; echo %q; echo %q) | crontab -`,
			rebootEntry, watchdogEntry,
		),
		"start": fmt.Sprintf(
			`. %s/.env && nohup %s/%s >> %s/%s.log 2>&1 < /dev/null &`,
			dir, dir, bin, dir, bin,
		),
		"stop":    fmt.Sprintf("pkill -f %s/%s", dir, bin),
		"restart": fmt.Sprintf("pkill -f %s/%s 2>/dev/null; sleep 1; . %s/.env && nohup %s/%s >> %s/%s.log 2>&1 < /dev/null &", dir, bin, dir, dir, bin, dir, bin),
		"reload":  fmt.Sprintf("pkill -HUP -f %s/%s", dir, bin),
		"status":  health,
		"enable":  "echo 'crontab: already enabled via crontab deploy'",
		"disable": fmt.Sprintf("crontab -l | grep -v %s | crontab -", bin),
	}
}

// BusyPath returns the .busy signal file path for a target.
func BusyPath(dir string) string {
	return dir + "/.busy"
}
