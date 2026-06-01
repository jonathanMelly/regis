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

// RenderTree renders the compact tree view for rdiff (Level2).
// Scenario names are indented group headers; status symbol appears immediately left of cue name.
// showDiff controls whether text diffs appear; showStdout controls stdout/stderr.
// level enables ANSI color on status symbols when >= Level2.
// An optional ManifestInfo shows the last deployed release below the header rule.
func RenderTree(results []cue.Result, target string, total time.Duration,
	showDiff bool, showStdout bool, level Level, manifest ...*ManifestInfo) string {
	var minfo *ManifestInfo
	if len(manifest) > 0 {
		minfo = manifest[0]
	}

	// Group results by scenario name in first-seen order.
	type group struct {
		label string
		rows  []cue.Result
	}
	var groups []group
	groupIdx := map[string]int{}
	for _, r := range results {
		label := r.ScenarioDesc
		if label == "" {
			label = r.ScenarioName
		}
		if i, ok := groupIdx[r.ScenarioName]; ok {
			groups[i].rows = append(groups[i].rows, r)
		} else {
			groupIdx[r.ScenarioName] = len(groups)
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

	// Header rule with inline summary: "target ────── N changed · M unchanged   Xs"
	sb.WriteString(treeRule(target+" ") + "  " + summary + "\n")

	// Manifest line (when available).
	if minfo != nil {
		fmt.Fprintf(&sb, "  deployed: %s  %s  %s\n",
			minfo.Release,
			minfo.DeployedAt.Format("2006-01-02 15:04"),
			minfo.DeployedBy,
		)
	}

	// Tree body: blank line before each scenario header, then cue rows.
	for _, g := range groups {
		sb.WriteString("\n")
		fmt.Fprintf(&sb, "  %s\n", g.label)
		for _, r := range g.rows {
			fmt.Fprintf(&sb, "    %s  %s\n", treeStatusSymbol(r, level), r.CueName)
		}
	}

	// Details section: one sub-header per scenario that has details.
	type scenarioDetail struct {
		label string
		lines []string
	}
	var detailSections []scenarioDetail
	for _, g := range groups {
		var gLines []string
		for _, r := range g.rows {
			gLines = append(gLines, cueDetailLines(r, showDiff, showStdout, minfo)...)
		}
		if len(gLines) > 0 {
			detailSections = append(detailSections, scenarioDetail{g.label, gLines})
		}
	}
	if len(detailSections) > 0 {
		sb.WriteString("\n")
		sb.WriteString(treeRule("── details ") + "\n")
		for _, ds := range detailSections {
			sb.WriteString(treeRule("  "+ds.label+" ") + "\n")
			for _, l := range ds.lines {
				fmt.Fprintf(&sb, "%s\n", l)
			}
		}
	}

	return sb.String()
}

// CueDetailLines is the exported accessor for cueDetailLines — used by the tui package.
func CueDetailLines(r cue.Result, showDiff bool, showStdout bool, minfo *ManifestInfo) []string {
	return cueDetailLines(r, showDiff, showStdout, minfo)
}
