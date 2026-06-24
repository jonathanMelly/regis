// internal/config/validate.go
package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ServiceID returns the canonical identifier for a service cue —
// service_name, service_file basename, or binary name.
// Returns "" when the cue has no service identifier.
// For binary cues with managed_by:, call ServiceID(ProjectManagedBy(cr)) instead.
func ServiceID(cr CueRef) string {
	if cr.ServiceName != "" {
		return cr.ServiceName
	}
	if cr.ServiceFile != "" {
		return strings.TrimSuffix(filepath.Base(cr.ServiceFile), ".service")
	}
	if cr.Binary != "" {
		return cr.Binary
	}
	return ""
}

// ProjectManagedBy returns a copy of cr with ManagedBy fields projected onto the
// service-level fields (Manager, ServiceFile, ServiceName, Health, Commands, Sudo, Binary).
// For crontab, Binary is derived from filepath.Base(cr.Dest) when ManagedBy.ServiceName is empty.
// Returns cr unchanged when ManagedBy is nil.
func ProjectManagedBy(cr CueRef) CueRef {
	if cr.ManagedBy == nil {
		return cr
	}
	m := cr.ManagedBy
	cr.Manager = m.Manager
	cr.ServiceFile = m.ServiceFile
	cr.ServiceName = m.ServiceName
	cr.Health = m.Health
	cr.Commands = m.Commands
	cr.Sudo = cr.Sudo || m.Sudo
	// Crontab: derive binary name from dest when no explicit service_name override.
	if m.Manager == "crontab" && cr.Binary == "" && m.ServiceName == "" {
		cr.Binary = filepath.Base(cr.Dest)
	}
	return cr
}

// KnownNatures is the single authoritative set of valid cue natures.
// Add an entry here when implementing a new executor.
var KnownNatures = map[string]bool{
	"binary":   true,
	"config":   true,
	"secret":   true,
	"action":   true,
	"generate": true,
	"render":   true,
	"pack":     true,
	"service":  true,
}

// fileNatures is the set of natures that deploy files (not commands).
var fileNatures = map[string]bool{
	"binary": true,
	"config": true,
	"secret": true,
	"render": true,
	"pack":   true,
}

// Validate checks the config for semantic errors and applies nature inference.
// Returns all errors found; callers typically act on the first.
func Validate(c *Config) []error {
	var errs []error
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	if len(c.Targets) == 0 {
		add("config must define at least one target")
	}
	for _, t := range c.Targets {
		if t.Name == "" {
			add("target missing 'name'")
		}
		if t.Host == "" {
			add("target %q missing 'host'", t.Name)
		}
		if t.User == "" {
			add("target %q missing 'user'", t.Name)
		}
		if t.Dir == "" {
			add("target %q missing 'dir'", t.Name)
		}
	}

	for scName, sc := range c.Scenarios {
		for _, req := range sc.Requires {
			if _, ok := c.Scenarios[req]; !ok {
				add("scenario %q requires %q which is not defined", scName, req)
			}
		}
		updated := sc
		scNameLower := strings.ToLower(scName)
		for i, cr := range sc.Cues {
			updated.Cues[i].ScenarioName = scNameLower
			if cr.ScenarioRef != "" {
				if _, ok := c.Scenarios[cr.ScenarioRef]; !ok {
					add("scenario %q: cue references undefined scenario %q", scName, cr.ScenarioRef)
				}
				continue
			}
			// Infer cue name from scenario name when the scenario has exactly one
			// inline cue with no explicit name — single-cue scenarios are common enough
			// that the scenario name is the natural cue identifier.
			if cr.Name == "" && len(sc.Cues) == 1 {
				cr.Name = scName
				updated.Cues[i].Name = scName
			}
			if cr.Name == "" {
				add("scenario %q: cue at index %d missing 'name'", scName, i)
				continue
			}
			nature, err := inferNature(cr)
			if err != nil {
				add("scenario %q cue %q: %v", scName, cr.Name, err)
				continue
			}
			updated.Cues[i].Nature = nature

			if nature == "service" && cr.Manager == "systemd" && cr.ServiceFile == "" && cr.ServiceName == "" {
				add("scenario %q cue %q: systemd service requires service_file: or service_name:", scName, cr.Name)
			}
			if nature == "service" && cr.Manager == "crontab" && cr.Binary == "" {
				add("scenario %q cue %q: crontab service requires binary:", scName, cr.Name)
			}
			if nature == "binary" && cr.ManagedBy != nil {
				m := cr.ManagedBy
				if m.Manager == "systemd" && m.ServiceFile == "" && m.ServiceName == "" {
					add("scenario %q cue %q: managed_by systemd requires service_file: or service_name:", scName, cr.Name)
				}
				if m.Manager == "crontab" && cr.Dest == "" && m.ServiceName == "" {
					add("scenario %q cue %q: managed_by crontab requires dest: (binary name for crontab entry)", scName, cr.Name)
				}
			}

			if cr.Git {
				if len(cr.Src) > 0 {
					add("scenario %q cue %q: git: true and src: are mutually exclusive", scName, cr.Name)
				}
				if nature != "pack" {
					add("scenario %q cue %q: git: true requires nature: pack", scName, cr.Name)
				}
			}

			if cr.Compensation != nil && cr.Compensation.Defer && cr.Shell == "" {
				add("scenario %q cue %q: compensation: defer requires a shell: command to re-run", scName, cr.Name)
			}
		}
		for _, cr := range sc.Checks {
			if cr.ScenarioRef != "" {
				if _, ok := c.Scenarios[cr.ScenarioRef]; !ok {
					add("scenario %q: check references undefined scenario %q", scName, cr.ScenarioRef)
				}
			}
		}
		for _, cr := range sc.Compensate {
			if cr.ScenarioRef != "" {
				if _, ok := c.Scenarios[cr.ScenarioRef]; !ok {
					add("scenario %q: compensate: block references undefined scenario %q", scName, cr.ScenarioRef)
				}
			}
		}
		c.Scenarios[scName] = updated
	}

	// Collect all service IDs (after natures have been inferred above).
	svcIDs := map[string]bool{}
	for _, sc := range c.Scenarios {
		for _, cr := range sc.Cues {
			if cr.Nature == "service" {
				if id := ServiceID(cr); id != "" {
					svcIDs[id] = true
				}
			} else if cr.Nature == "binary" && cr.ManagedBy != nil {
				if id := ServiceID(ProjectManagedBy(cr)); id != "" {
					svcIDs[id] = true
				}
			}
		}
	}

	// Validate post: shorthand references.
	checkPostRef := func(pa PostAction, ctx string) {
		for _, prefix := range []string{"restart:", "reload:", "deploy:"} {
			if strings.HasPrefix(pa.Cmd, prefix) {
				svcName := strings.TrimPrefix(pa.Cmd, prefix)
				if !svcIDs[svcName] {
					add("%s: %q references unknown service — use the binary: or service_name: value of a service cue", ctx, pa.Cmd)
				}
			}
		}
	}
	for scName, sc := range c.Scenarios {
		if sc.Post.Cmd != "" {
			checkPostRef(sc.Post, fmt.Sprintf("scenario %q post", scName))
		}
		for _, cr := range sc.Cues {
			if cr.ScenarioRef != "" {
				continue
			}
			if cr.Post.Cmd != "" {
				checkPostRef(cr.Post, fmt.Sprintf("scenario %q cue %q post", scName, cr.Name))
			}
		}
	}

	return errs
}

// ValidateWarnings returns non-blocking advisory warnings for a validated config.
// Call after Validate succeeds.
func ValidateWarnings(c *Config) []string {
	var warns []string
	for scName, sc := range c.Scenarios {
		for _, cr := range sc.Cues {
			if cr.ScenarioRef != "" || cr.Compensation == nil || !cr.Compensation.Enabled {
				continue
			}
			if fileNatures[cr.Nature] {
				if cr.Compensation.Shell == "" && !cr.Compensation.Interactive {
					warns = append(warns, fmt.Sprintf(
						"scenario %q cue %q (%s): compensation: true has no effect on file natures — file state is not automatically restored; use compensation: \"shell\" if you need a command, or see 'regis state hint'",
						scName, cr.Name, cr.Nature,
					))
				} else {
					warns = append(warns, fmt.Sprintf(
						"scenario %q cue %q (%s): compensation: on a file nature will run the shell, but file state is not automatically restored — see 'regis state hint' for full recovery guidance",
						scName, cr.Name, cr.Nature,
					))
				}
			}
		}
	}
	return warns
}

func inferNature(c CueRef) (string, error) {
	if c.Nature != "" {
		if !KnownNatures[c.Nature] {
			return "", fmt.Errorf("unknown nature %q", c.Nature)
		}
		return c.Nature, nil
	}
	hasSrc := len(c.Src) > 0
	hasShell := c.Shell != ""
	hasDest := c.Dest != ""
	hasPreserve := len(c.Preserve) > 0

	switch {
	case c.Git && !hasShell:
		return "pack", nil
	case hasShell && !hasSrc && hasDest:
		return "render", nil
	case hasShell && !hasSrc:
		return "action", nil
	case hasSrc && hasPreserve:
		return "secret", nil
	case hasSrc && !hasPreserve:
		return "", fmt.Errorf("cannot infer nature from src+dest alone — specify nature: binary or nature: config explicitly")
	default:
		return "", fmt.Errorf("cannot determine nature — specify nature: binary, config, secret, or action")
	}
}
