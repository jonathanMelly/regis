// cmd/regis/cmd/env.go
package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"git.disroot.org/jmy/regis/internal/config"
	"github.com/spf13/cobra"
)

var envVarRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// secretKeywords are substrings (case-insensitive) that trigger masking.
var secretKeywords = []string{"TOKEN", "KEY", "PASSWORD", "SECRET", "PASS", "CRED"}

func isSecretKey(name string) bool {
	upper := strings.ToUpper(name)
	for _, kw := range secretKeywords {
		if strings.Contains(upper, kw) {
			return true
		}
	}
	return false
}

// collectVarRefs scans all string fields in cfg for ${VAR} references.
// Returns a sorted, deduplicated slice of variable names.
func collectVarRefs(cfg *config.Config) []string {
	seen := make(map[string]bool)

	scan := func(s string) {
		for _, m := range envVarRe.FindAllStringSubmatch(s, -1) {
			seen[m[1]] = true
		}
	}

	for _, t := range cfg.Targets {
		scan(t.Host)
		scan(t.User)
		scan(t.Dir)
		scan(t.Dotenv)
	}
	for _, sc := range cfg.Scenarios {
		for _, cr := range sc.Cues {
			scan(cr.Shell)
			scan(cr.If)
			scan(cr.Dest)
			for _, s := range cr.Src {
				scan(s)
			}
			scan(cr.Post.Cmd)
		}
		for _, cr := range sc.Checks {
			scan(cr.Shell)
			scan(cr.If)
			scan(cr.Dest)
			for _, s := range cr.Src {
				scan(s)
			}
		}
	}
	for _, pp := range cfg.Pre {
		scan(pp.Cmd)
	}
	for _, pp := range cfg.Post {
		scan(pp.Cmd)
	}

	names := make([]string, 0, len(seen))
	for k := range seen {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func newEnvCommand(gf *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "env",
		Short: "show env files and variable sources for a target",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			targetName := gf.Target

			// Load config. Use LoadForTarget when a target is named.
			var cfg *config.Config
			var err error
			if targetName != "" {
				cfg, err = config.LoadForTarget(gf.File, targetName)
			} else {
				cfg, err = config.Load(gf.File)
			}
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			// Resolve target struct.
			var target *config.Target
			if targetName != "" {
				for i := range cfg.Targets {
					if cfg.Targets[i].Name == targetName {
						target = &cfg.Targets[i]
						break
					}
				}
				if target == nil {
					return fmt.Errorf("target %q not found", targetName)
				}
			} else if len(cfg.Targets) > 0 {
				target = &cfg.Targets[0]
				targetName = target.Name
			}

			// Build merged dotenv map (no shell env).
			var dotenv map[string]string
			var globalEnv map[string]string // .env.local values
			var targetEnv map[string]string // target-specific values
			var targetEnvPath string
			var globalEnvPath string

			if target != nil {
				dotenv, err = config.BuildEnvForTarget(cfg, target)
				if err != nil {
					return fmt.Errorf("build env: %w", err)
				}

				// Resolve paths for source attribution display.
				globalEnvPath = filepath.Join(cfg.BaseDir, ".env.local")

				if target.Dotenv != "" {
					if filepath.IsAbs(target.Dotenv) {
						targetEnvPath = target.Dotenv
					} else {
						targetEnvPath = filepath.Join(cfg.BaseDir, target.Dotenv)
					}
				} else {
					targetEnvPath = filepath.Join(cfg.BaseDir, ".env."+target.Name)
				}

				// Load each layer separately for source attribution.
				if m, readErr := readDotenv(globalEnvPath); readErr == nil {
					globalEnv = m
				}
				if m, readErr := readDotenv(targetEnvPath); readErr == nil {
					targetEnv = m
				}
			}

			// Print env files section.
			label := targetName
			if label == "" {
				label = "(no target)"
			}
			fmt.Fprintf(out, "Env files (target: %s):\n", label)

			idx := 1
			if globalEnvPath != "" {
				if _, statErr := os.Stat(globalEnvPath); statErr == nil {
					fmt.Fprintf(out, "  %d. %-40s global\n", idx, globalEnvPath)
					idx++
				}
			}
			if targetEnvPath != "" {
				if _, statErr := os.Stat(targetEnvPath); statErr == nil {
					fmt.Fprintf(out, "  %d. %-40s target\n", idx, targetEnvPath)
					idx++
				}
			}

			fmt.Fprintln(out)

			// Collect all ${VAR} references from the raw YAML bytes so we see
			// references before interpolation substitutes them away.
			var varNames []string
			varNames, err = collectVarRefsFromFile(gf.File)
			if err != nil {
				// Fallback: scan the already-interpolated config struct.
				varNames = collectVarRefs(cfg)
			}

			if len(varNames) == 0 {
				fmt.Fprintln(out, "No variables referenced in regis.yml.")
				return nil
			}

			fmt.Fprintln(out, "Variables referenced in regis.yml:")

			// Column widths.
			maxName := len("Variable")
			maxVal := len("Value")
			for _, name := range varNames {
				if len(name) > maxName {
					maxName = len(name)
				}
			}

			// Pre-compute values for width calculation.
			type row struct {
				name   string
				value  string
				source string
			}
			rows := make([]row, 0, len(varNames))
			for _, name := range varNames {
				var value, source string

				if shellVal, ok := os.LookupEnv(name); ok {
					value = shellVal
					source = "shell"
				} else if targetEnv != nil {
					if v, ok := targetEnv[name]; ok {
						value = v
						source = filepath.Base(targetEnvPath)
					}
				}
				if source == "" && globalEnv != nil {
					if v, ok := globalEnv[name]; ok {
						value = v
						source = filepath.Base(globalEnvPath)
					}
				}
				if source == "" {
					// Check merged dotenv as fallback (covers cases where global or target env was found)
					if dotenv != nil {
						if v, ok := dotenv[name]; ok {
							value = v
							source = ".env"
						}
					}
				}
				if source == "" {
					value = "(unset)"
					source = "-"
				} else if isSecretKey(name) {
					value = "***"
				}

				rows = append(rows, row{name, value, source})
				if len(value) > maxVal {
					maxVal = len(value)
				}
			}

			fmt.Fprintf(out, "  %-*s  %-*s  %s\n", maxName, "Variable", maxVal, "Value", "Source")
			fmt.Fprintf(out, "  %s  %s  %s\n", strings.Repeat("-", maxName), strings.Repeat("-", maxVal), strings.Repeat("-", 8))
			for _, r := range rows {
				fmt.Fprintf(out, "  %-*s  %-*s  %s\n", maxName, r.name, maxVal, r.value, r.source)
			}

			return nil
		},
	}
}

// readDotenv is a thin wrapper that reads a dotenv file and returns the map.
// Returns an error if the file doesn't exist or can't be parsed.
func readDotenv(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Parse key=value lines manually to avoid importing godotenv here.
	// We rely on the fact that config/interpolate.go already has godotenv.
	// For simplicity, delegate to godotenv via the config package indirectly
	// by reading an environment from file.
	_ = data
	return parseDotenvFile(path)
}

// parseDotenvFile parses a .env file without loading into the process environment.
// Handles basic KEY=VALUE and KEY="VALUE" forms; ignores comments and blank lines.
func parseDotenvFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		// Strip surrounding quotes.
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') ||
				(val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		m[key] = val
	}
	return m, nil
}

// collectVarRefsFromFile reads all YAML source files referenced by the config
// at path and returns a sorted, deduplicated list of ${VAR} variable names found
// in the raw file bytes — before any interpolation occurs.
func collectVarRefsFromFile(path string) ([]string, error) {
	seen := make(map[string]bool)

	scanFile := func(p string) error {
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range envVarRe.FindAllSubmatch(data, -1) {
			seen[string(m[1])] = true
		}
		return nil
	}

	if err := scanFile(path); err != nil {
		return nil, err
	}

	// Also scan sibling regis.*.yml files (mirrors parseAndMerge logic).
	dir := filepath.Dir(path)
	siblings, _ := filepath.Glob(filepath.Join(dir, "regis.*.yml"))
	for _, sib := range siblings {
		if sib != path {
			_ = scanFile(sib) // best-effort
		}
	}

	names := make([]string, 0, len(seen))
	for k := range seen {
		names = append(names, k)
	}
	sort.Strings(names)
	return names, nil
}
