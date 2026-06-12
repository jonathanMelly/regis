// internal/score/dag.go
package score

import (
	"fmt"
	"sort"
	"strings"

	"git.disroot.org/jmy/regis/internal/config"
)

const defaultMaxDepth = 5

// RenderTree renders each scenario as a card with ├─/└─ branches.
// Scenario refs (* name) are expanded inline up to maxDepth levels deep (default 5).
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
func RenderTree(cfg *config.Config, filter []string, sortMode string, maxDepth ...int) string {
	md := defaultMaxDepth
	if len(maxDepth) > 0 && maxDepth[0] >= 0 {
		md = maxDepth[0]
	}

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

		writeScenarioTreeDepth(&sb, sc, cfg, "  ", 0, md, []string{name})

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

// scenarioItems returns all display labels for a scenario in YAML declaration order:
// requirements (** name) first, then cues in the order they appear in the config.
func scenarioItems(sc config.Scenario) []string {
	var items []string
	for _, req := range sc.Requires {
		items = append(items, "** "+req)
	}
	for _, cr := range sc.Cues {
		items = append(items, cueLabel(cr))
	}
	return items
}

// treeEntry is one line in the scenario tree, with optional expansion metadata.
type treeEntry struct {
	label       string
	scenarioRef string   // non-empty when this is a * scenario ref (expandable)
	narrowCue   string   // single-cue filter from NarrowCue
	cueNames    []string // multi-cue filter from CueNames
}

// collectTreeEntries returns display entries for a scenario in declaration order:
// requires lines (** name) first, then cues.
func collectTreeEntries(sc config.Scenario) []treeEntry {
	var entries []treeEntry
	for _, req := range sc.Requires {
		entries = append(entries, treeEntry{label: "** " + req})
	}
	for _, cr := range sc.Cues {
		entries = append(entries, treeEntry{
			label:       cueLabel(cr),
			scenarioRef: cr.ScenarioRef,
			narrowCue:   cr.NarrowCue,
			cueNames:    []string(cr.CueNames),
		})
	}
	return entries
}

// writeScenarioTreeDepth writes ├─/└─ branch lines for sc, recursively expanding
// scenario refs up to maxDepth levels. indent is prepended before the branch character.
// ancestors tracks scenario names on the current expansion path for cycle detection.
func writeScenarioTreeDepth(sb *strings.Builder, sc config.Scenario, cfg *config.Config, indent string, depth, maxDepth int, ancestors []string) {
	entries := collectTreeEntries(sc)
	for i, entry := range entries {
		isLast := i == len(entries)-1
		branch := "├─ "
		if isLast {
			branch = "└─ "
		}
		sb.WriteString(indent + branch + entry.label + "\n")

		if entry.scenarioRef == "" || depth >= maxDepth {
			continue
		}
		for _, a := range ancestors {
			if a == entry.scenarioRef {
				goto next
			}
		}
		if refSc, ok := cfg.Scenarios[entry.scenarioRef]; ok {
			refSc = narrowScenario(refSc, entry.narrowCue, entry.cueNames)
			if len(refSc.Cues) > 0 || len(refSc.Requires) > 0 {
				cont := "│   "
				if isLast {
					cont = "    "
				}
				writeScenarioTreeDepth(sb, refSc, cfg, indent+cont, depth+1, maxDepth, append(ancestors, entry.scenarioRef))
			}
		}
	next:
	}
}

// narrowScenario returns a copy of sc filtered to the named cue(s) when a narrow ref is used.
func narrowScenario(sc config.Scenario, narrowCue string, cueNames []string) config.Scenario {
	if narrowCue == "" && len(cueNames) == 0 {
		return sc
	}
	sc.Requires = nil
	if narrowCue != "" {
		for _, cr := range sc.Cues {
			if cr.Name == narrowCue {
				sc.Cues = []config.CueRef{cr}
				return sc
			}
		}
		sc.Cues = nil
		return sc
	}
	set := make(map[string]bool, len(cueNames))
	for _, n := range cueNames {
		set[n] = true
	}
	filtered := sc.Cues[:0:0]
	for _, cr := range sc.Cues {
		if set[cr.Name] {
			filtered = append(filtered, cr)
		}
	}
	sc.Cues = filtered
	return sc
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
	return "legend  ! binary  # config  § secret  @ action  ~ render  % pack  + generate  & service  * ref  ** requires\n"
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
