// internal/tui/rdiff.go
package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"git.disroot.org/jmy/regis/internal/cue"
	"git.disroot.org/jmy/regis/internal/output"
)

// rdiffItemKind distinguishes visible list item types.
type rdiffItemKind int

const (
	rdiffKindScenario rdiffItemKind = iota
	rdiffKindCue
	rdiffKindDetail
)

// rdiffVisItem is one entry in the flat visible list.
type rdiffVisItem struct {
	kind        rdiffItemKind
	scenarioIdx int
	cueIdx      int    // -1 for scenario items
	line        string // pre-formatted content for kindDetail
}

// rdiffScenario holds per-scenario state for the TUI model.
type rdiffScenario struct {
	name        string
	label       string
	results     []cue.Result
	expanded    bool
	cueExpanded []bool
	detailLines [][]string // pre-computed per-cue sub-detail lines
}

// rdiffModel is the Bubble Tea model for the interactive rdiff TUI.
type rdiffModel struct {
	scenarios   []rdiffScenario
	visible     []rdiffVisItem
	cursor      int
	searchMode  bool
	searchQuery string
	target      string
	total       time.Duration
	verbose     bool
	minfo       *output.ManifestInfo
	width       int
	height      int
	quitting    bool
}

// cueSubDetailLines returns the sub-lines for a result (without the cue-name header),
// trimming the 4-space indent that cueDetailLines uses for sub-lines.
func cueSubDetailLines(r cue.Result, verbose bool, minfo *output.ManifestInfo) []string {
	lines := output.CueDetailLines(r, true, verbose, minfo)
	if len(lines) == 0 {
		return nil
	}
	// First line is always the cue-name header (starts with "  "); skip it.
	lines = lines[1:]
	// Trim leading 4 spaces from sub-lines so they render cleanly under the cue row.
	result := make([]string, len(lines))
	for i, l := range lines {
		result[i] = strings.TrimPrefix(l, "    ")
	}
	return result
}

// rdiffStatusSymbol returns the status symbol for a result.
func rdiffStatusSymbol(r cue.Result) string {
	switch r.Status {
	case cue.StatusChanged:
		if !r.LocalMtime.IsZero() && !r.RemoteMtime.IsZero() && r.RemoteMtime.After(r.LocalMtime) {
			return "↓"
		}
		return "↑"
	case cue.StatusEqual:
		return "="
	case cue.StatusFailed:
		return "✕"
	case cue.StatusSkipped:
		return "/"
	}
	return "?"
}

func newRdiffModel(
	results []cue.Result,
	target string,
	total time.Duration,
	verbose bool,
	minfo *output.ManifestInfo,
) rdiffModel {
	// Group results by scenario name in first-seen order.
	type groupEntry struct {
		name  string
		label string
		rows  []cue.Result
	}
	var groups []groupEntry
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
			groups = append(groups, groupEntry{r.ScenarioName, label, []cue.Result{r}})
		}
	}

	scenarios := make([]rdiffScenario, len(groups))
	for i, g := range groups {
		subLines := make([][]string, len(g.rows))
		for j, r := range g.rows {
			subLines[j] = cueSubDetailLines(r, verbose, minfo)
		}
		scenarios[i] = rdiffScenario{
			name:        g.name,
			label:       g.label,
			results:     g.rows,
			expanded:    false,
			cueExpanded: make([]bool, len(g.rows)),
			detailLines: subLines,
		}
	}

	m := rdiffModel{
		scenarios: scenarios,
		target:    target,
		total:     total,
		verbose:   verbose,
		minfo:     minfo,
		width:     80,
		height:    24,
	}
	m.visible = m.buildVisible()
	return m
}

// buildVisible rebuilds the flat visible item list from current expand/search state.
func (m rdiffModel) buildVisible() []rdiffVisItem {
	var items []rdiffVisItem
	query := strings.ToLower(m.searchQuery)
	for si, sc := range m.scenarios {
		scenarioMatch := query == "" ||
			strings.Contains(strings.ToLower(sc.name), query) ||
			strings.Contains(strings.ToLower(sc.label), query)
		if query != "" && !scenarioMatch {
			hasMatch := false
			for _, r := range sc.results {
				if strings.Contains(strings.ToLower(r.CueName), query) {
					hasMatch = true
					break
				}
			}
			if !hasMatch {
				continue
			}
		}
		items = append(items, rdiffVisItem{kind: rdiffKindScenario, scenarioIdx: si, cueIdx: -1})
		if !sc.expanded {
			continue
		}
		for ci, r := range sc.results {
			if query != "" && !scenarioMatch && !strings.Contains(strings.ToLower(r.CueName), query) {
				continue
			}
			items = append(items, rdiffVisItem{kind: rdiffKindCue, scenarioIdx: si, cueIdx: ci})
			if !sc.cueExpanded[ci] {
				continue
			}
			for _, line := range sc.detailLines[ci] {
				items = append(items, rdiffVisItem{kind: rdiffKindDetail, scenarioIdx: si, cueIdx: ci, line: line})
			}
		}
	}
	return items
}

func (m rdiffModel) Init() tea.Cmd { return nil }

func (m rdiffModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		if m.searchMode {
			return m.updateSearch(msg)
		}
		return m.updateNormal(msg)
	}
	return m, nil
}

func (m rdiffModel) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.searchMode = false
		m.searchQuery = ""
		m.visible = m.buildVisible()
		m.cursor = rdiffClamp(m.cursor, 0, len(m.visible)-1)
	case tea.KeyBackspace:
		if len(m.searchQuery) > 0 {
			runes := []rune(m.searchQuery)
			m.searchQuery = string(runes[:len(runes)-1])
			m.visible = m.buildVisible()
			m.cursor = 0
		}
	case tea.KeyEnter:
		m.searchMode = false
		m.visible = m.buildVisible()
		m.cursor = rdiffClamp(m.cursor, 0, len(m.visible)-1)
	case tea.KeyRunes:
		m.searchQuery += msg.String()
		m.visible = m.buildVisible()
		m.cursor = 0
	}
	return m, nil
}

func (m rdiffModel) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyUp:
		if m.cursor > 0 {
			m.cursor--
		}
	case tea.KeyDown:
		if m.cursor < len(m.visible)-1 {
			m.cursor++
		}
	case tea.KeyCtrlP:
		if m.cursor > 0 {
			m.cursor--
		}
	case tea.KeyCtrlN:
		if m.cursor < len(m.visible)-1 {
			m.cursor++
		}
	case tea.KeyRight, tea.KeyTab, tea.KeyEnter:
		m = m.toggleExpand()
	case tea.KeyLeft, tea.KeyBackspace:
		m = m.collapseItem()
	case tea.KeyShiftTab:
		m = m.jumpPrevScenario()
	case tea.KeyCtrlC:
		m.quitting = true
		return m, tea.Quit
	case tea.KeyRunes:
		switch msg.String() {
		case "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "j":
			if m.cursor < len(m.visible)-1 {
				m.cursor++
			}
		case "c", "-":
			m = m.compactAll()
		case "d", "+":
			m = m.expandAll()
		case "/":
			m.searchMode = true
			m.searchQuery = ""
		case "q":
			m.quitting = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m rdiffModel) toggleExpand() rdiffModel {
	if len(m.visible) == 0 {
		return m
	}
	item := m.visible[m.cursor]
	switch item.kind {
	case rdiffKindScenario:
		m.scenarios[item.scenarioIdx].expanded = !m.scenarios[item.scenarioIdx].expanded
	case rdiffKindCue:
		si, ci := item.scenarioIdx, item.cueIdx
		if len(m.scenarios[si].detailLines[ci]) > 0 {
			m.scenarios[si].cueExpanded[ci] = !m.scenarios[si].cueExpanded[ci]
		}
	}
	m.visible = m.buildVisible()
	m.cursor = rdiffClamp(m.cursor, 0, len(m.visible)-1)
	return m
}

func (m rdiffModel) collapseItem() rdiffModel {
	if len(m.visible) == 0 {
		return m
	}
	item := m.visible[m.cursor]
	switch item.kind {
	case rdiffKindScenario:
		m.scenarios[item.scenarioIdx].expanded = false
	case rdiffKindCue:
		m.scenarios[item.scenarioIdx].cueExpanded[item.cueIdx] = false
	case rdiffKindDetail:
		si, ci := item.scenarioIdx, item.cueIdx
		m.scenarios[si].cueExpanded[ci] = false
		m.visible = m.buildVisible()
		// Move cursor up to the parent cue row.
		for i, v := range m.visible {
			if v.kind == rdiffKindCue && v.scenarioIdx == si && v.cueIdx == ci {
				m.cursor = i
				return m
			}
		}
		return m
	}
	m.visible = m.buildVisible()
	m.cursor = rdiffClamp(m.cursor, 0, len(m.visible)-1)
	return m
}

func (m rdiffModel) jumpPrevScenario() rdiffModel {
	for i := m.cursor - 1; i >= 0; i-- {
		if m.visible[i].kind == rdiffKindScenario {
			m.cursor = i
			return m
		}
	}
	return m
}

func (m rdiffModel) compactAll() rdiffModel {
	for i := range m.scenarios {
		m.scenarios[i].expanded = false
		for j := range m.scenarios[i].cueExpanded {
			m.scenarios[i].cueExpanded[j] = false
		}
	}
	m.visible = m.buildVisible()
	m.cursor = rdiffClamp(m.cursor, 0, len(m.visible)-1)
	return m
}

func (m rdiffModel) expandAll() rdiffModel {
	for i := range m.scenarios {
		m.scenarios[i].expanded = true
		for j := range m.scenarios[i].cueExpanded {
			m.scenarios[i].cueExpanded[j] = true
		}
	}
	m.visible = m.buildVisible()
	m.cursor = rdiffClamp(m.cursor, 0, len(m.visible)-1)
	return m
}

func (m rdiffModel) View() string {
	if m.quitting {
		return ""
	}

	// Reserve lines for: rule, summary, blank, help (+ search prompt if active).
	reserved := 4
	if m.searchMode {
		reserved = 5
	}
	listHeight := m.height - reserved
	if listHeight < 1 {
		listHeight = 1
	}

	listLines := m.renderList()
	start, end := m.scrollWindow(listLines, listHeight)

	var sb strings.Builder
	for _, l := range listLines[start:end] {
		sb.WriteString(l + "\n")
	}

	// Rule + summary.
	ruleWidth := m.width - 1
	if ruleWidth < 10 {
		ruleWidth = 49
	}
	sb.WriteString(strings.Repeat("─", ruleWidth) + "\n")

	changed, equal, failed := rdiffCountResults(m.allResults())
	if failed > 0 {
		fmt.Fprintf(&sb, "%d changed · %d unchanged · %d failed   %.1fs\n",
			changed, equal, failed, m.total.Seconds())
	} else {
		fmt.Fprintf(&sb, "%d changed · %d unchanged   %.1fs\n",
			changed, equal, m.total.Seconds())
	}

	// Help or search prompt.
	if m.searchMode {
		fmt.Fprintf(&sb, "\n/ %s█\n", m.searchQuery)
	} else {
		sb.WriteString("\n↑↓ navigate  →/enter expand  ← collapse  +/- all  / search  q quit\n")
	}

	return sb.String()
}

func (m rdiffModel) renderList() []string {
	const ansiReverse = "\033[7m"
	const ansiReset = "\033[m"

	var lines []string
	for vi, item := range m.visible {
		isCursor := vi == m.cursor
		var line string
		switch item.kind {
		case rdiffKindScenario:
			sc := m.scenarios[item.scenarioIdx]
			arrow := "○"
			if sc.expanded {
				arrow = "●"
			}
			changed := 0
			for _, r := range sc.results {
				if r.Status == cue.StatusChanged {
					changed++
				}
			}
			info := "all in sync"
			if changed > 0 {
				info = fmt.Sprintf("%d changed", changed)
			}
			line = fmt.Sprintf("%s %s   %s", arrow, sc.label, info)
		case rdiffKindCue:
			r := m.scenarios[item.scenarioIdx].results[item.cueIdx]
			sym := rdiffStatusSymbol(r)
			line = fmt.Sprintf("    %s  %s", sym, r.CueName)
		case rdiffKindDetail:
			line = "       " + item.line
		}
		if isCursor {
			line = ansiReverse + line + ansiReset
		}
		lines = append(lines, line)
	}
	return lines
}

func (m rdiffModel) scrollWindow(lines []string, height int) (start, end int) {
	n := len(lines)
	if n <= height {
		return 0, n
	}
	start = m.cursor - height/2
	if start < 0 {
		start = 0
	}
	end = start + height
	if end > n {
		end = n
		start = end - height
		if start < 0 {
			start = 0
		}
	}
	return start, end
}

func (m rdiffModel) allResults() []cue.Result {
	var all []cue.Result
	for _, sc := range m.scenarios {
		all = append(all, sc.results...)
	}
	return all
}

func rdiffCountResults(results []cue.Result) (changed, equal, failed int) {
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

func rdiffClamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// RunRdiffTUI runs the interactive Bubble Tea TUI for rdiff (Level3).
// On quit, prints the static RenderTree output to stdout.
func RunRdiffTUI(results []cue.Result, target string, total time.Duration, verbose bool, level output.Level, minfo *output.ManifestInfo) error {
	m := newRdiffModel(results, target, total, verbose, minfo)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	if err != nil {
		return err
	}
	fmt.Print(output.RenderTree(results, target, total, true, verbose, level, minfo))
	return nil
}
