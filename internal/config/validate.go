// internal/config/validate.go
package config

import (
	"fmt"
	"strings"
)

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
