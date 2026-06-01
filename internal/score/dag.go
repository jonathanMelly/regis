// internal/score/dag.go
package score

import (
	"fmt"
	"sort"
	"strings"

	"git.disroot.org/jmy/regis/internal/config"
)

// RenderTree renders each scenario as a card with ├─/└─ branches.
// Order within each scenario: inline cues → scenario refs (*) → requirements (**).
// Uppercase-initial scenarios (public entry-points) appear first, then lowercase
// building blocks, both groups in YAML declaration order (sortMode="yaml") or
// alphabetically (sortMode="alpha").
// filter limits output to named scenarios (nil = all).
//
// Example:
//
//	checks   Pre-deploy checks
//	  ├─ @ go-version  (local)
//	  └─ @ nginx-remote
//
//	build    Build binaries
//	  ├─ @ bins  (local)
//	  └─ ** checks
func RenderTree(cfg *config.Config, filter []string, sortMode string) string {
	var sb strings.Builder

	if len(cfg.Targets) > 0 {
		fmt.Fprintf(&sb, "regis score — %s\n\n", cfg.Targets[0].Name)
	}

	names := applyFilter(SortedScenarioNames(cfg, sortMode), filter)
	for i, name := range names {
		sc := cfg.Scenarios[name]

		if sc.Describe != "" {
			fmt.Fprintf(&sb, "%-16s %s\n", name, sc.Describe)
		} else {
			sb.WriteString(name + "\n")
		}

		writeScenarioTree(&sb, sc)

		if i < len(names)-1 {
			sb.WriteString("\n")
		}
	}

	writeServices(&sb, cfg)
	sb.WriteString("\n" + legendLine())
	return sb.String()
}

// RenderFlow shows pipeline stages (execution order) first, then per-scenario cue detail.
// filter limits output to named scenarios (nil = all).
func RenderFlow(cfg *config.Config, filter []string, sortMode string) string {
	var sb strings.Builder

	if len(cfg.Targets) > 0 {
		fmt.Fprintf(&sb, "regis score — %s\n\n", cfg.Targets[0].Name)
	}

	allNames := SortedScenarioNames(cfg, sortMode)
	names := applyFilter(allNames, filter)
	depths := scenarioDepths(cfg.Scenarios, allNames)

	maxDepth := 0
	for _, d := range depths {
		if d > maxDepth {
			maxDepth = d
		}
	}

	sb.WriteString("pipeline\n")
	for d := 0; d <= maxDepth; d++ {
		first := true
		for _, name := range names {
			if depths[name] != d {
				continue
			}
			sc := cfg.Scenarios[name]
			needs := ""
			if len(sc.Requires) > 0 {
				needs = "   needs: " + strings.Join([]string(sc.Requires), ", ")
			}
			if first {
				fmt.Fprintf(&sb, "  stage %-2d  %s%s\n", d, name, needs)
				first = false
			} else {
				fmt.Fprintf(&sb, "            %s%s\n", name, needs)
			}
		}
	}

	sb.WriteString("\nscenarios\n")
	for _, name := range names {
		sc := cfg.Scenarios[name]
		desc := ""
		if sc.Describe != "" {
			desc = "  " + sc.Describe
		}
		fmt.Fprintf(&sb, "  %s%s\n", name, desc)
		items := scenarioItems(sc)
		for _, item := range items {
			fmt.Fprintf(&sb, "    %s\n", item)
		}
	}

	writeServices(&sb, cfg)
	sb.WriteString("\n" + legendLine())
	return sb.String()
}

// RenderCompact renders one line per scenario listing cues inline: name: cue1, cue2, ...
// Ordering: inline cues, scenario refs (*), requirements (**) — same as tree.
// filter limits output to named scenarios (nil = all).
func RenderCompact(cfg *config.Config, filter []string, sortMode string) string {
	var sb strings.Builder

	if len(cfg.Targets) > 0 {
		fmt.Fprintf(&sb, "regis score — %s\n\n", cfg.Targets[0].Name)
	}

	names := applyFilter(SortedScenarioNames(cfg, sortMode), filter)
	for _, name := range names {
		sc := cfg.Scenarios[name]
		items := scenarioItems(sc)
		line := strings.Join(items, ",  ")
		if sc.Describe != "" {
			fmt.Fprintf(&sb, "%-16s %s\n    %s\n", name, sc.Describe, line)
		} else {
			fmt.Fprintf(&sb, "%-16s %s\n", name, line)
		}
	}

	sb.WriteString("\n" + legendLine())
	return sb.String()
}

// RenderDAG delegates to RenderTree with no filter (backward compatibility).
func RenderDAG(cfg *config.Config) string { return RenderTree(cfg, nil, "yaml") }

// applyFilter returns only names present in filter (preserving order). nil = no filter.
func applyFilter(names, filter []string) []string {
	if len(filter) == 0 {
		return names
	}
	set := make(map[string]bool, len(filter))
	for _, f := range filter {
		set[f] = true
	}
	out := make([]string, 0, len(filter))
	for _, n := range names {
		if set[n] {
			out = append(out, n)
		}
	}
	return out
}

// SortedScenarioNames returns scenario names with uppercase-initial (public) scenarios first,
// then lowercase building blocks. Within each group the order follows sortMode:
//   - "yaml" (default): YAML declaration order (falls back to alpha if ScenarioNames is empty)
//   - "alpha": alphabetical
func SortedScenarioNames(cfg *config.Config, sortMode string) []string {
	base := cfg.ScenarioNames
	if sortMode == "alpha" || len(base) == 0 {
		base = sortedKeys(cfg.Scenarios)
	}
	var upper, lower []string
	for _, n := range base {
		if len(n) > 0 && n[0] >= 'A' && n[0] <= 'Z' {
			upper = append(upper, n)
		} else {
			lower = append(lower, n)
		}
	}
	if sortMode == "alpha" {
		sort.Strings(upper)
		sort.Strings(lower)
	}
	return append(upper, lower...)
}

// sortedKeys returns map keys sorted alphabetically.
func sortedKeys(scenarios map[string]config.Scenario) []string {
	names := make([]string, 0, len(scenarios))
	for name := range scenarios {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// scenarioDepths computes the execution depth of each scenario (longest requires chain).
func scenarioDepths(scenarios map[string]config.Scenario, names []string) map[string]int {
	depths := make(map[string]int, len(names))
	var depth func(name string, visited map[string]bool) int
	depth = func(name string, visited map[string]bool) int {
		if visited[name] {
			return 0
		}
		if d, ok := depths[name]; ok {
			return d
		}
		visited[name] = true
		sc, ok := scenarios[name]
		if !ok || len(sc.Requires) == 0 {
			depths[name] = 0
			return 0
		}
		max := 0
		for _, req := range sc.Requires {
			d := depth(req, visited) + 1
			if d > max {
				max = d
			}
		}
		depths[name] = max
		return max
	}
	for _, name := range names {
		depth(name, make(map[string]bool))
	}
	return depths
}

// scenarioItems returns all display labels for a scenario in logical sequence:
// 1. requirements (** name) — what must run first
// 2. inline cues — what this scenario does
// 3. scenario refs (* name) — embedded scenario expansions
func scenarioItems(sc config.Scenario) []string {
	var items []string
	for _, req := range sc.Requires {
		items = append(items, "** "+req)
	}
	for _, cr := range sc.Cues {
		if cr.ScenarioRef == "" {
			items = append(items, cueLabel(cr))
		}
	}
	for _, cr := range sc.Cues {
		if cr.ScenarioRef != "" {
			items = append(items, cueLabel(cr))
		}
	}
	return items
}

// writeScenarioTree writes the ├─/└─ branch lines for a scenario.
func writeScenarioTree(sb *strings.Builder, sc config.Scenario) {
	items := scenarioItems(sc)
	for i, label := range items {
		branch := "  ├─ "
		if i == len(items)-1 {
			branch = "  └─ "
		}
		sb.WriteString(branch + label + "\n")
	}
}

// cueLabel formats a single cue ref for display.
func cueLabel(cr config.CueRef) string {
	if cr.ScenarioRef != "" {
		ref := cr.ScenarioRef
		if cr.NarrowCue != "" {
			ref += "." + cr.NarrowCue
		} else if len(cr.CueNames) > 0 {
			ref += ".{" + strings.Join([]string(cr.CueNames), ",") + "}"
		}
		return "* " + ref
	}
	sym := natureSymbol(cr.Nature, cr.Local)
	parts := []string{sym + " " + cr.Name}
	if cr.Local {
		parts = append(parts, "(local)")
	}
	if len(cr.Src) > 0 && cr.Dest != "" {
		parts = append(parts, cr.Src[0]+" -> "+cr.Dest)
	} else if cr.Dest != "" {
		parts = append(parts, "-> "+cr.Dest)
	}
	if cr.Post.Cmd != "" {
		parts = append(parts, ">> "+cr.Post.Cmd)
	}
	return strings.Join(parts, "  ")
}

func writeServices(sb *strings.Builder, cfg *config.Config) {
	// Collect all service cues across scenarios in declaration order.
	var svcs []config.CueRef
	seen := make(map[string]bool)
	for _, name := range cfg.ScenarioNames {
		sc := cfg.Scenarios[name]
		for _, cr := range sc.Cues {
			if cr.ScenarioRef != "" || cr.Nature != "service" {
				continue
			}
			if !seen[cr.Name] {
				svcs = append(svcs, cr)
				seen[cr.Name] = true
			}
		}
	}
	if len(svcs) == 0 {
		return
	}
	sb.WriteString("\nservices\n")
	for _, cr := range svcs {
		health := ""
		if cr.Health != "" {
			health = "  ? " + cr.Health
		}
		fmt.Fprintf(sb, "  & %-14s [%s]%s\n", cr.Name, cr.Manager, health)
	}
}

func legendLine() string {
	// § = CP437 char 21, @ = arobase, & = ampersand — all ConEmu-safe
	return "legend  ! binary  # config  § secret  @ action  ~ render  + generate  & service  * ref  ** requires\n"
}

// natureSymbol returns a terminal-safe symbol for each cue nature.
// Symbols chosen for ConEmu/Windows compatibility (ASCII + CP437 range).
//   & binary   — & (logical/binary operator)
//   # config   — # (sharp = config marker convention)
//   § secret   — § (section sign, CP437 char 21 = sensitive/private)
//   @ action   — @ (at/arobase = address/command target)
//   ~ render   — ~ (tilde = generated/template)
//   + generate — + (plus = creates new)
func natureSymbol(nature string, local bool) string {
	switch nature {
	case "binary":
		return "!"
	case "config":
		return "#"
	case "secret":
		return "§"
	case "render":
		return "~"
	case "pack":
		return "%"
	case "generate":
		return "+"
	case "action":
		return "@"
	case "service":
		return "&"
	}
	return "?"
}
