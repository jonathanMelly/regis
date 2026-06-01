// internal/config/loader.go
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Load reads the primary config file, auto-discovers regis.*.yml siblings,
// applies explicit includes, merges all, interpolates variables, and validates.
func Load(path string) (*Config, error) {
	base, err := parseAndMerge(path)
	if err != nil {
		return nil, err
	}
	base.BaseDir = filepath.Dir(path)

	if err := Interpolate(base); err != nil {
		return nil, err
	}
	if errs := Validate(base); len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return base, nil
}

// LoadForTarget reads and merges config files, sets BaseDir, finds the named target,
// calls InterpolateForTarget for that target only, and validates.
func LoadForTarget(path string, targetName string) (*Config, error) {
	base, err := parseAndMerge(path)
	if err != nil {
		return nil, err
	}
	base.BaseDir = filepath.Dir(path)

	// Find the named target.
	var target *Target
	for i := range base.Targets {
		if base.Targets[i].Name == targetName {
			target = &base.Targets[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("target %q not found in %s", targetName, path)
	}

	if err := InterpolateForTarget(base, target); err != nil {
		return nil, err
	}
	if errs := Validate(base); len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return base, nil
}

// parseAndMerge reads the primary config file, auto-discovers regis.*.yml siblings,
// and applies explicit includes. Does NOT interpolate or validate.
func parseAndMerge(path string) (*Config, error) {
	base, err := parseFile(path)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}
	base.SourceFiles = []string{path}
	dir := filepath.Dir(path)

	// Auto-discover regis.*.yml siblings (sorted by filepath.Glob)
	siblings, err := filepath.Glob(filepath.Join(dir, "regis.*.yml"))
	if err != nil {
		return nil, err
	}
	for _, sib := range siblings {
		if sib == path {
			continue
		}
		overlay, err := parseFile(sib)
		if err != nil {
			return nil, fmt.Errorf("auto-include %s: %w", sib, err)
		}
		if err := mergeInto(base, overlay, sib); err != nil {
			return nil, err
		}
		base.SourceFiles = append(base.SourceFiles, sib)
	}

	// Explicit includes (after auto-discovered siblings, in declared order)
	for _, inc := range base.Includes {
		incPath := inc
		if !filepath.IsAbs(incPath) {
			incPath = filepath.Join(dir, inc)
		}
		// Skip if already loaded (e.g. auto-discovered as a sibling)
		alreadyLoaded := false
		for _, sf := range base.SourceFiles {
			if sf == incPath {
				alreadyLoaded = true
				break
			}
		}
		if alreadyLoaded {
			continue
		}
		overlay, err := parseFile(incPath)
		if err != nil {
			return nil, fmt.Errorf("include %s: %w", incPath, err)
		}
		if err := mergeInto(base, overlay, incPath); err != nil {
			return nil, err
		}
		base.SourceFiles = append(base.SourceFiles, incPath)
	}

	return base, nil
}

// parseFile reads and unmarshals one YAML file into a Config.
func parseFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Scenarios == nil {
		cfg.Scenarios = make(map[string]Scenario)
	}
	for name, sc := range cfg.Scenarios {
		sc.SourceFile = path
		cfg.Scenarios[name] = sc
	}
	return &cfg, nil
}
