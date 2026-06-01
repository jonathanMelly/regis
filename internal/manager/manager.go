// internal/manager/manager.go
package manager

import (
	"strings"
	"git.disroot.org/jmy/regis/internal/config"
)

// ExpandCommands returns a map of action → command string for the service cue.
// For built-in managers (systemd, crontab) defaults are provided.
// For custom managers, Commands: map is used as-is.
func ExpandCommands(cr config.CueRef, tgt config.Target) map[string]string {
	var base map[string]string
	switch cr.Manager {
	case "systemd":
		base = systemdDefaults(cr)
	case "crontab":
		base = crontabDefaults(cr, tgt)
	default:
		base = make(map[string]string)
	}
	// Apply overrides from Commands:
	for action, cmd := range cr.Commands {
		base[action] = ExpandTemplate(cmd, cr, tgt, base)
	}
	return base
}

// ExpandTemplate replaces placeholders in a command template.
// Static placeholders: {name}, {binary}, {dir}, {service_file}.
// Dynamic placeholders: {start}, {stop}, {restart}, {reload}, etc. — each
// expands to the corresponding entry in baseCmds (the pre-override base
// commands), allowing overrides to "call super": e.g. "nginx -t && {reload}".
func ExpandTemplate(template string, cr config.CueRef, tgt config.Target, baseCmds map[string]string) string {
	pairs := []string{
		"{name}", cr.Name,
		"{binary}", cr.Binary,
		"{dir}", tgt.Dir,
		"{service_file}", cr.ServiceFile,
	}
	for action, cmd := range baseCmds {
		pairs = append(pairs, "{"+action+"}", cmd)
	}
	return strings.NewReplacer(pairs...).Replace(template)
}
