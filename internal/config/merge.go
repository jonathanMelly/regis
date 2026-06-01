// internal/config/merge.go
package config

import "fmt"

// mergeInto merges overlay into base in place following spec §3.1 rules.
func mergeInto(base, overlay *Config, overlayFile string) error {
	if base.Scenarios == nil {
		base.Scenarios = make(map[string]Scenario)
	}
	for name, sc := range overlay.Scenarios {
		if existing, ok := base.Scenarios[name]; ok {
			return fmt.Errorf("scenario %q defined in both %s and %s", name, existing.SourceFile, overlayFile)
		}
		base.Scenarios[name] = sc
	}
	base.ScenarioNames = append(base.ScenarioNames, overlay.ScenarioNames...)

	tgtSeen := make(map[string]bool)
	for _, t := range base.Targets {
		tgtSeen[t.Name] = true
	}
	for _, t := range overlay.Targets {
		if tgtSeen[t.Name] {
			return fmt.Errorf("target %q already defined; conflict in %s", t.Name, overlayFile)
		}
		base.Targets = append(base.Targets, t)
	}

	// Includes is intentionally not propagated from overlays.
	// Only the primary file's includes: list is honoured.
	base.Pre = append(base.Pre, overlay.Pre...)
	base.Post = append(base.Post, overlay.Post...)

	// Merge defaults.env: first file defining a key wins.
	if len(overlay.Defaults.Env) > 0 {
		if base.Defaults.Env == nil {
			base.Defaults.Env = make(map[string]string)
		}
		for k, v := range overlay.Defaults.Env {
			if _, exists := base.Defaults.Env[k]; !exists {
				base.Defaults.Env[k] = v
			}
		}
	}
	return nil
}
