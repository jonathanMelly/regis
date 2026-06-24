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
	interval := cr.Interval
	if interval == "" {
		interval = "*/3 * * * *"
	}

	rebootEntry := fmt.Sprintf(
		"@reboot . %s/.env && nohup %s/%s >> %s/%s.log 2>&1 < /dev/null &",
		dir, dir, bin, dir, bin,
	)
	watchdogEntry := fmt.Sprintf(
		`%s [ -f %s/.busy ] || %s || (. %s/.env && nohup %s/%s >> %s/%s.log 2>&1 < /dev/null &)`,
		interval, dir, health, dir, dir, bin, dir, bin,
	)

	return map[string]string{
		// Replace any existing entries for this binary before adding fresh ones.
		// grep -vF strips both the reboot and watchdog lines (both contain dir/bin),
		// so re-running deploy always installs the current entry text.
		"deploy": fmt.Sprintf(
			`(crontab -l 2>/dev/null | grep -vF %q; echo %q; echo %q) | crontab -`,
			dir+"/"+bin, rebootEntry, watchdogEntry,
		),
		// watchdog_line is the exact cron line used by checkEnabled to detect staleness.
		"watchdog_line": watchdogEntry,
		"start": fmt.Sprintf(
			`. %s/.env && nohup %s/%s >> %s/%s.log 2>&1 < /dev/null &`,
			dir, dir, bin, dir, bin,
		),
		"stop": fmt.Sprintf("pkill -f %s/%s", dir, bin),
		// Wait up to 5 s for the old process to exit before starting the new one.
		// Using a pgrep loop instead of a fixed sleep avoids the race where the
		// old process is still alive when the replacement binary is launched.
		"restart": fmt.Sprintf(
			"pkill -f %s/%s 2>/dev/null; for _i in 1 2 3 4 5 6 7 8 9 10; do pgrep -f %s/%s >/dev/null 2>&1 || break; sleep 0.5; done; . %s/.env && nohup %s/%s >> %s/%s.log 2>&1 </dev/null &",
			dir, bin, dir, bin, dir, dir, bin, dir, bin,
		),
		"reload":     fmt.Sprintf("pkill -HUP -f %s/%s", dir, bin),
		"status":     health,
		"enable":     "echo 'crontab: already enabled via crontab deploy'",
		"disable":    fmt.Sprintf("crontab -l | grep -v %s | crontab -", bin),
		"busy_set":   fmt.Sprintf("touch %s", BusyPath(dir)),
		"busy_clear": fmt.Sprintf("rm -f %s", BusyPath(dir)),
	}
}

// BusyPath returns the .busy signal file path for a target.
func BusyPath(dir string) string {
	return dir + "/.busy"
}
