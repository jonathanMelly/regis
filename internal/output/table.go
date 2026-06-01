// internal/output/table.go
package output

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"git.disroot.org/jmy/regis/internal/cue"
)

const (
	ansiReset  = "\033[0m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiRed    = "\033[31m"
	ansiDim    = "\033[2m"
)

// ColorStatus wraps label in ANSI color based on status; no-op at Level1.
func ColorStatus(status cue.Status, label string, level Level) string {
	if level < Level2 {
		return label
	}
	switch status {
	case cue.StatusChanged:
		return ansiGreen + label + ansiReset
	case cue.StatusEqual:
		return ansiDim + label + ansiReset
	case cue.StatusFailed:
		return ansiRed + label + ansiReset
	case cue.StatusSkipped:
		return ansiYellow + label + ansiReset
	}
	return label
}

// vlen returns the visual (rune) width of s — correct for ASCII and multi-byte UTF-8.
func vlen(s string) int {
	return utf8.RuneCountInString(s)
}

// rdiffSymbol returns ↑ (local newer) or ↓ (remote newer) for a StatusChanged result.
// Falls back to ↑ when mtime data is unavailable.
func rdiffSymbol(r cue.Result) string {
	if r.LocalMtime.IsZero() || r.RemoteMtime.IsZero() {
		return "↑"
	}
	if r.LocalMtime.After(r.RemoteMtime) {
		return "↑"
	}
	return "↓"
}

// ManifestInfo holds release manifest data for display in rdiff output.
type ManifestInfo struct {
	Release    string
	DeployedAt time.Time
	DeployedBy string
}

// RenderTable renders Level2+ output: a framed table with Unicode box-drawing borders.
// Results are grouped by scenario using ScenarioDesc (falls back to ScenarioName).
// deployed=true uses Status.Applied() labels (post-run); false uses Status.String() (rdiff).
// Details (diffs, MD5, errors) appear inline after each scenario's cue rows when present.
// verbose=true shows diffs and stdout for changed cues.
// An optional ManifestInfo shows the last deployed release above the first scenario separator.
func RenderTable(results []cue.Result, target string, total time.Duration, deployed bool, level Level, verbose bool, manifest ...*ManifestInfo) string {
	const col2 = 9  // Size column inner width
	const col3 = 6  // Time column inner width
	const col4 = 10 // Status column inner width

	// Compute col1 width using rune count so multi-byte labels align correctly.
	col1 := 26
	for _, r := range results {
		label := r.ScenarioDesc
		if label == "" {
			label = r.ScenarioName
		}
		if w := 1 + vlen(label) + 1; w > col1 {
			col1 = w
		}
		if w := 3 + vlen(r.CueName) + 1; w > col1 {
			col1 = w
		}
	}

	// padR right-pads s to w visual columns. Uses rune count, not byte length.
	padR := func(s string, w int) string {
		rw := vlen(s)
		if rw >= w {
			return string([]rune(s)[:w])
		}
		return s + strings.Repeat(" ", w-rw)
	}

	centerStr := func(s string, w int) string {
		rw := vlen(s)
		if rw >= w {
			return string([]rune(s)[:w])
		}
		tot := w - rw
		left := tot / 2
		return strings.Repeat(" ", left) + s + strings.Repeat(" ", tot-left)
	}

	// Right-align with 1-space right margin; empty string → all spaces.
	rightAlign1 := func(s string, w int) string {
		if s == "" {
			return strings.Repeat(" ", w)
		}
		inner := w - 1
		rw := vlen(s)
		if rw > inner {
			s = string([]rune(s)[:inner])
			rw = inner
		}
		return strings.Repeat(" ", inner-rw) + s + " "
	}

	hbar := func(l, m, r string) string {
		return l + strings.Repeat("─", col1) +
			m + strings.Repeat("─", col2) +
			m + strings.Repeat("─", col3) +
			m + strings.Repeat("─", col4) + r
	}
	top := hbar("┌", "┬", "┐")
	sep := hbar("├", "┼", "┤")
	bot := hbar("└", "┴", "┘")

	// Full-width separator and row for inline detail sections (no column dividers).
	fullWidth := col1 + col2 + col3 + col4 + 3
	detailSep := "├" + strings.Repeat("─", fullWidth) + "┤"
	detailRow := func(content string) string {
		rw := vlen(content)
		if rw < fullWidth {
			content += strings.Repeat(" ", fullWidth-rw)
		} else {
			content = string([]rune(content)[:fullWidth])
		}
		return "│" + content + "│"
	}

	dataRow := func(c1, c2, c3, c4 string) string {
		return "│" + c1 + "│" + c2 + "│" + c3 + "│" + c4 + "│"
	}

	var sb strings.Builder
	sb.WriteString(top + "\n")
	sb.WriteString(dataRow(
		padR(" Scenario / Cue", col1),
		centerStr("Size", col2),
		centerStr("Time", col3),
		centerStr("Status", col4),
	) + "\n")

	// Group results by ScenarioName in first-seen order.
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

	blank2 := strings.Repeat(" ", col2)
	blank3 := strings.Repeat(" ", col3)
	blank4 := strings.Repeat(" ", col4)

	// Optional manifest header row (rdiff only, when manifest available).
	var minfo *ManifestInfo
	if len(manifest) > 0 {
		minfo = manifest[0]
	}
	if minfo != nil && !deployed {
		deployLine := fmt.Sprintf(" Deployed: %s  %s  %s",
			minfo.Release,
			minfo.DeployedAt.Format("2006-01-02 15:04"),
			minfo.DeployedBy,
		)
		sb.WriteString(sep + "\n")
		sb.WriteString(dataRow(padR(deployLine, col1), blank2, blank3, blank4) + "\n")
	}

	for _, g := range groups {
		sb.WriteString(sep + "\n")
		sb.WriteString(dataRow(padR(" "+g.label, col1), blank2, blank3, blank4) + "\n")
		for _, r := range g.rows {
			size := ""
			if r.Size > 0 {
				size = formatSize(r.Size)
			}
			elapsed := ""
			if r.Elapsed > 0 {
				elapsed = fmt.Sprintf("%.1fs", r.Elapsed.Seconds())
			}
			statusLabel := r.Status.String()
			if deployed {
				statusLabel = r.Status.Applied()
			} else if r.Status == cue.StatusChanged {
				statusLabel = rdiffSymbol(r)
			}
			sb.WriteString(dataRow(
				padR("   "+r.CueName, col1),
				rightAlign1(size, col2),
				centerStr(elapsed, col3),
				ColorStatus(r.Status, centerStr(statusLabel, col4), level),
			) + "\n")
		}
		// Inline detail section for this scenario (errors, diffs, binary MD5, etc.)
		var gDetail []string
		for _, r := range g.rows {
			gDetail = append(gDetail, cueDetailLines(r, true, verbose, minfo)...)
		}
		if len(gDetail) > 0 {
			sb.WriteString(detailSep + "\n")
			for _, l := range gDetail {
				sb.WriteString(detailRow(l) + "\n")
			}
		}
	}

	sb.WriteString(bot + "\n")

	// Summary line
	changed, equal, failed := countResults(results)
	sb.WriteString("\n")
	verb := "deployed"
	if !deployed {
		verb = "changed"
	}
	if failed > 0 {
		fmt.Fprintf(&sb, "FAILED    %s   %d %s · %d unchanged · %d failed   %.1fs\n",
			target, changed, verb, equal, failed, total.Seconds())
	} else if deployed {
		fmt.Fprintf(&sb, "DEPLOYED  %s   %d deployed · %d unchanged   %.1fs\n",
			target, changed, equal, total.Seconds())
	} else {
		fmt.Fprintf(&sb, "RDIFF     %s   %d changed · %d unchanged   %.1fs\n",
			target, changed, equal, total.Seconds())
	}
	return sb.String()
}

// cueDetailLines returns displayable detail lines for a single result.
// Returns nil when nothing should be shown.
// Lines use 2-space indent for the cue header, 4-space for sub-lines.
// Used both by AppendDetails (appended below the table) and RenderTable (inline per scenario).
// showDiff controls whether text diffs are included; showStdout controls stdout/stderr output.
func cueDetailLines(r cue.Result, showDiff bool, showStdout bool, minfo *ManifestInfo) []string {
	var lines []string

	// Manifest drift — always shown when detected.
	if r.ManifestDrift {
		lines = append(lines, fmt.Sprintf("  ⚠ %s: remote file differs from last deploy manifest", r.CueName))
		if minfo != nil {
			lines = append(lines, fmt.Sprintf("     last deployed: %s  (%s, %s)",
				truncate(r.ManifestChecksum, 12),
				minfo.Release,
				minfo.DeployedAt.Format("2006-01-02 15:04"),
			))
		} else if r.ManifestChecksum != "" {
			lines = append(lines, fmt.Sprintf("     last deployed: %s", truncate(r.ManifestChecksum, 12)))
		}
		lines = append(lines, fmt.Sprintf("     remote now:    %s", truncate(r.RemoteMD5, 12)))
	}

	switch r.Status {
	case cue.StatusFailed:
		detail := resultDetail(r)
		if detail != "" {
			dlines := strings.Split(strings.TrimRight(detail, "\n"), "\n")
			lines = append(lines, fmt.Sprintf("  %s: %s", r.CueName, dlines[0]))
			for _, l := range dlines[1:] {
				lines = append(lines, "    "+l)
			}
		}
	case cue.StatusSkipped:
		if r.Stdout != "" {
			lines = append(lines, fmt.Sprintf("  %s: skipped — %s", r.CueName, r.Stdout))
		} else if showStdout {
			lines = append(lines, fmt.Sprintf("  %s: skipped — action outcome cannot be determined without executing", r.CueName))
		}
	case cue.StatusChanged:
		// Binary cues: always show path + mtime + MD5 comparison when available.
		if r.Nature == "binary" && r.LocalMD5 != "" {
			lines = append(lines, fmt.Sprintf("  %s:", r.CueName))
			if r.LocalPath != "" {
				lines = append(lines, fmt.Sprintf("    %s → %s", r.LocalPath, r.RemotePath))
			}
			lines = append(lines, fmt.Sprintf("    local : %s  %s", r.LocalMtime.Format("2006-01-02 15:04"), truncate(r.LocalMD5, 12)))
			lines = append(lines, fmt.Sprintf("    remote: %s  %s", r.RemoteMtime.Format("2006-01-02 15:04"), truncate(r.RemoteMD5, 12)))
			break
		}
		if showDiff && r.Diff != "" {
			lines = append(lines, fmt.Sprintf("  %s:", r.CueName))
			for _, l := range strings.Split(strings.TrimRight(r.Diff, "\n"), "\n") {
				lines = append(lines, "  "+l)
			}
		}
		if showStdout {
			if out := strings.TrimSpace(r.Stdout + r.Stderr); out != "" {
				for _, l := range strings.Split(out, "\n") {
					lines = append(lines, "  "+l)
				}
			}
		}
	}
	return lines
}

// AppendDetails returns extra lines for failed/skipped/verbose results, appended below the table.
// An optional ManifestInfo provides release/date context for drift messages.
func AppendDetails(results []cue.Result, verbose bool, manifest ...*ManifestInfo) string {
	var minfo *ManifestInfo
	if len(manifest) > 0 {
		minfo = manifest[0]
	}
	var sb strings.Builder
	for _, r := range results {
		lines := cueDetailLines(r, verbose, verbose, minfo)
		if len(lines) > 0 {
			sb.WriteString("\n")
			for _, l := range lines {
				fmt.Fprintf(&sb, "%s\n", l)
			}
		}
	}
	return sb.String()
}

// truncate shortens s to n characters, appending "..." if truncated.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// resultDetail returns the most useful error string for a failed result.
// Prefers raw stderr (human-readable command output) over the wrapped error.
func resultDetail(r cue.Result) string {
	if s := strings.TrimSpace(r.Stderr); s != "" {
		return s
	}
	if r.Err != nil {
		return r.Err.Error()
	}
	return ""
}

// RenderPlain renders Level1 output: one line per cue, no decoration.
// Intended for CI / non-TTY; no colors, no borders.
// deployed=true uses Status.Applied() labels (post-run); false uses Status.String() (rdiff).
func RenderPlain(results []cue.Result, target string, total time.Duration, deployed bool) string {
	var sb strings.Builder
	for _, r := range results {
		status := r.Status.String()
		if deployed {
			status = r.Status.Applied()
		}
		size := ""
		if r.Size > 0 {
			size = formatSize(r.Size)
		}
		elapsed := ""
		if r.Elapsed > 0 {
			elapsed = fmt.Sprintf("%.1fs", r.Elapsed.Seconds())
		}
		fmt.Fprintf(&sb, "[%s] %-24s %-8s %-10s %s\n",
			target, r.CueName, status, size, elapsed)
	}
	// Summary line
	changed, equal, failed := countResults(results)
	verb := "deployed"
	if !deployed {
		verb = "changed"
	}
	if failed > 0 {
		fmt.Fprintf(&sb, "FAILED    %s  %d %s  %d unchanged  %d failed  %.1fs\n",
			target, changed, verb, equal, failed, total.Seconds())
	} else if deployed {
		fmt.Fprintf(&sb, "DEPLOYED  %s  %d deployed  %d unchanged  %.1fs\n",
			target, changed, equal, total.Seconds())
	} else {
		fmt.Fprintf(&sb, "RDIFF     %s  %d changed  %d unchanged  %.1fs\n",
			target, changed, equal, total.Seconds())
	}
	return sb.String()
}

func countResults(results []cue.Result) (changed, equal, failed int) {
	for _, r := range results {
		switch r.Status {
		case cue.StatusChanged:
			changed++
		case cue.StatusEqual:
			equal++
		case cue.StatusFailed:
			failed++
		}
	}
	return
}

func formatSize(bytes int64) string {
	switch {
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(bytes)/(1<<10))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
