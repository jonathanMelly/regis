// internal/config/interpolate.go
package config

import (
	"os"
	"path/filepath"
	"regexp"

	"github.com/joho/godotenv"
)

var varRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// InterpolateString replaces ${VAR_NAME} using shell env (priority) then dotenv map.
// Unknown variables are left unchanged.
func InterpolateString(s string, dotenv map[string]string) string {
	return varRe.ReplaceAllStringFunc(s, func(m string) string {
		name := m[2 : len(m)-1]
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		if v, ok := dotenv[name]; ok {
			return v
		}
		return m
	})
}

// BuildEnvForTarget returns a merged env map for a target (does NOT modify config).
// Chain: .env.local is loaded first, then the target-specific dotenv overlays it.
// Shell env is NOT included here; it is applied at interpolation time via InterpolateString.
// Auto-discovery rule: if t.Dotenv != "" use that path; else use BaseDir/.env.<name>.
func BuildEnvForTarget(c *Config, t *Target) (map[string]string, error) {
	merged := make(map[string]string)

	// Layer 0: standard target variables — overridable by all dotenv layers.
	merged["TARGET_NAME"] = t.Name
	merged["TARGET_HOST"] = t.Host
	merged["TARGET_USER"] = t.User
	merged["TARGET_DIR"] = t.Dir
	if t.Port != "" {
		merged["TARGET_PORT"] = t.Port
	}

	// Layer 1: .env.local
	if c.BaseDir != "" {
		if m, err := godotenv.Read(filepath.Join(c.BaseDir, ".env.local")); err == nil {
			for k, v := range m {
				merged[k] = v
			}
		}
	}

	// Layer 2: target-specific dotenv (overlays .env.local)
	var targetEnvPath string
	if t.Dotenv != "" {
		// Explicit path: resolve relative to BaseDir if not absolute.
		if filepath.IsAbs(t.Dotenv) {
			targetEnvPath = t.Dotenv
		} else {
			targetEnvPath = filepath.Join(c.BaseDir, t.Dotenv)
		}
	} else {
		targetEnvPath = filepath.Join(c.BaseDir, ".env."+t.Name)
	}

	if m, err := godotenv.Read(targetEnvPath); err == nil {
		for k, v := range m {
			merged[k] = v
		}
	}

	return merged, nil
}

// InterpolateForTarget applies per-target variable substitution in-place.
// Env chain (highest priority first): shell env > target dotenv > .env.local.
// If t is nil, falls back to .env.local only (backward compat) and applies to ALL targets.
func InterpolateForTarget(c *Config, t *Target) error {
	var dotenv map[string]string

	if t == nil {
		// Backward-compat path: load .env.local only.
		if c.BaseDir != "" {
			if m, err := godotenv.Read(filepath.Join(c.BaseDir, ".env.local")); err == nil {
				dotenv = m
			}
		} else if len(c.SourceFiles) > 0 {
			// Fallback to SourceFiles[0] dir for callers that haven't set BaseDir.
			envFile := filepath.Join(filepath.Dir(c.SourceFiles[0]), ".env.local")
			if m, err := godotenv.Read(envFile); err == nil {
				dotenv = m
			}
		}
		fn := func(s string) string { return InterpolateString(s, dotenv) }

		for i, tgt := range c.Targets {
			c.Targets[i].Host = fn(tgt.Host)
			c.Targets[i].User = fn(tgt.User)
			c.Targets[i].Port = fn(tgt.Port)
			c.Targets[i].Dir = fn(tgt.Dir)
		}
		for name, sc := range c.Scenarios {
			for i, cr := range sc.Cues {
				sc.Cues[i].Shell = fn(cr.Shell)
				sc.Cues[i].If = fn(cr.If)
				sc.Cues[i].Dest = fn(cr.Dest)
				for j, src := range cr.Src {
					sc.Cues[i].Src[j] = fn(src)
				}
			}
			for i, cr := range sc.Checks {
				sc.Checks[i].Shell = fn(cr.Shell)
				sc.Checks[i].If = fn(cr.If)
				sc.Checks[i].Dest = fn(cr.Dest)
				for j, src := range cr.Src {
					sc.Checks[i].Src[j] = fn(src)
				}
			}
			c.Scenarios[name] = sc
		}
		for i, pp := range c.Pre {
			c.Pre[i].Cmd = fn(pp.Cmd)
		}
		for i, pp := range c.Post {
			c.Post[i].Cmd = fn(pp.Cmd)
		}
		return nil
	}

	// Per-target path: build merged dotenv for this specific target.
	env, err := BuildEnvForTarget(c, t)
	if err != nil {
		return err
	}
	fn := func(s string) string { return InterpolateString(s, env) }

	// Only interpolate fields for the matching target; other targets are left raw.
	for i, tgt := range c.Targets {
		if tgt.Name == t.Name {
			c.Targets[i].Host = fn(tgt.Host)
			c.Targets[i].User = fn(tgt.User)
			c.Targets[i].Port = fn(tgt.Port)
			c.Targets[i].Dir = fn(tgt.Dir)
		}
	}
	for name, sc := range c.Scenarios {
		for i, cr := range sc.Cues {
			sc.Cues[i].Shell = fn(cr.Shell)
			sc.Cues[i].If = fn(cr.If)
			sc.Cues[i].Dest = fn(cr.Dest)
			for j, src := range cr.Src {
				sc.Cues[i].Src[j] = fn(src)
			}
		}
		for i, cr := range sc.Checks {
			sc.Checks[i].Shell = fn(cr.Shell)
			sc.Checks[i].If = fn(cr.If)
			sc.Checks[i].Dest = fn(cr.Dest)
			for j, src := range cr.Src {
				sc.Checks[i].Src[j] = fn(src)
			}
		}
		c.Scenarios[name] = sc
	}
	for i, pp := range c.Pre {
		c.Pre[i].Cmd = fn(pp.Cmd)
	}
	for i, pp := range c.Post {
		c.Post[i].Cmd = fn(pp.Cmd)
	}
	return nil
}

// doc:interpolation
// Variables use ${VAR_NAME} syntax. Resolution order (highest priority first):
//   shell environment  >  target dotenv (.env.<name> auto-discovered)  >  .env.local  >  built-in target vars
// .env.local is loaded alongside regis.yml and shared across all targets.
// Per-target dotenv: .env.<target-name> is auto-loaded when that target is selected.
// Explicit override: set dotenv: path on a target entry.
// Shell environment always wins — use for CI and ephemeral secret injection.
// Built-in target variables (available in config files, service files, and templates):
//   ${TARGET_NAME}  — target name as defined in regis.yml
//   ${TARGET_HOST}  — SSH host
//   ${TARGET_USER}  — SSH user
//   ${TARGET_DIR}   — remote working directory (target.dir)
//   ${TARGET_PORT}  — SSH port (only when explicitly set)
// Built-in runtime variable (injected into every cue env at deploy start):
//   ${RELEASE_ID}   — unique deploy ID; format vYYYYMMDD-HHMMSS, or vYYYYMMDD-HHMMSS+<tag> when
//                     HEAD is on a git tag; consistent across all cues in a run; use in backup
//                     labels (backup --label=pre-${RELEASE_ID}) so rollback can find the right snapshot

// Interpolate applies variable substitution to all string fields in the Config.
// Loads .env.local from BaseDir (or the directory of the primary source file).
// Delegates to InterpolateForTarget(c, nil) for backward compatibility.
func Interpolate(c *Config) error {
	return InterpolateForTarget(c, nil)
}
