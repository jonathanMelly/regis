// internal/output/tree.go
package output

import (
	"fmt"
	"strings"
	"time"

	"git.disroot.org/jmy/regis/internal/cue"
)

const treeRuleWidth = 49

// treeRule returns a horizontal rule of treeRuleWidth chars, prefixed by prefix.
func treeRule(prefix string) string {
	pw := len([]rune(prefix))
	w := treeRuleWidth - pw
	if w < 0 {
		w = 0
	}
	return prefix + strings.Repeat("─", w)
}

// treeStatusSymbol returns the compact status symbol for the rdiff tree view,
// wrapped in ANSI color when level >= Level2.
func treeStatusSymbol(r cue.Result, level Level) string {
	var sym string
	switch r.Status {
	case cue.StatusChanged:
		sym = rdiffSymbol(r) // ↑ or ↓ based on mtime
	case cue.StatusEqual:
		sym = "="
	case cue.StatusFailed:
		sym = "✕"
	case cue.StatusSkipped:
		sym = "/"
	default:
		sym = "?"
	}
	return ColorStatus(r.Status, sym, level)
}

// RenderTree renders the compact tree view for rdiff/run (Level1 and Level2 static output).
// Scenario names are indented group headers; status symbol appears immediately left of cue name.
// Skipped cues appear inline (dimmed at Level2) — no separate footer.
// Detail lines (diffs, errors, warnings) appear directly below each cue.
// showDiff controls whether text diffs appear; showStdout controls stdout/stderr.
// level enables ANSI color on status symbols when >= Level2.
// An optional ManifestInfo shows the last deployed release below the header rule.
func RenderTree(results []cue.Result, target string, total time.Duration,
	showDiff bool, showStdout bool, level Level, manifest ...*ManifestInfo) string {
	var minfo *ManifestInfo
	if len(manifest) > 0 {
		minfo = manifest[0]
	}

	// Group results by top-level scenario in first-seen order.
	// GroupScenarioName (set by the runner) is the display owner — for ref-expanded cues
	// this is the top-level caller, not the referenced sub-scenario, so they all appear
	// together under one heading instead of fragmenting into per-sub-scenario sections.
	type group struct {
		label string
		rows  []cue.Result
	}
	var groups []group
	groupIdx := map[string]int{}
	for _, r := range results {
		key := r.GroupScenarioName
		if key == "" {
			key = r.ScenarioName
		}
		label := r.GroupScenarioDesc
		if label == "" {
			label = r.ScenarioDesc
		}
		if label == "" {
			label = key
		}
		if i, ok := groupIdx[key]; ok {
			groups[i].rows = append(groups[i].rows, r)
		} else {
			groupIdx[key] = len(groups)
			groups = append(groups, group{label: label, rows: []cue.Result{r}})
		}
	}

	// Summary computed upfront so it can appear inline on the header rule.
	changed, equal, failed := countResults(results)
	var summary string
	if failed > 0 {
		summary = fmt.Sprintf("%d changed · %d unchanged · %d failed   %.1fs", changed, equal, failed, total.Seconds())
	} else {
		summary = fmt.Sprintf("%d changed · %d unchanged   %.1fs", changed, equal, total.Seconds())
	}

	var sb strings.Builder

	// Header rule with inline summary.
	sb.WriteString(treeRule(target+" ") + "  " + summary + "\n")

	// Manifest line (when available).
	if minfo != nil {
		fmt.Fprintf(&sb, "  deployed: %s  %s  %s\n",
			minfo.ID,
			minfo.DeployedAt.Format("2006-01-02 15:04"),
			minfo.DeployedBy,
		)
	}

	// Tree body: blank line before each scenario header, then cue rows with inline details.
	for _, g := range groups {
		sb.WriteString("\n")
		fmt.Fprintf(&sb, "  %s\n", g.label)
		for _, r := range g.rows {
			name := r.CueName
			if r.FileTotal > 0 {
				name += fmt.Sprintf("  %d/%d", r.FileChanged, r.FileTotal)
			}
			sym := treeStatusSymbol(r, level)
			// Skipped cues: dim at Level2.
			cueLabel := name
			if r.Status == cue.StatusSkipped && level >= Level2 {
				cueLabel = ansiDim + name + ansiReset
			}
			// Append warning marker when there are warnings.
			warnSuffix := ""
			if len(r.Warnings) > 0 {
				warnSuffix = "  ⚠"
			}
			fmt.Fprintf(&sb, "    %s  %s%s\n", sym, cueLabel, warnSuffix)
			// Inline detail lines immediately below the cue.
			for _, l := range cueDetailLines(r, showDiff, showStdout, minfo) {
				fmt.Fprintf(&sb, "      %s\n", l)
			}
		}
	}

	return sb.String()
}

// CueDetailLines is the exported accessor for cueDetailLines — used by the tui package.
// For the split exec/info subtrees use CueExecLines and CueInfoLines directly.
func CueDetailLines(r cue.Result, showDiff bool, showStdout bool, minfo *ManifestInfo) []string {
	return cueDetailLines(r, showDiff, showStdout, minfo)
}
