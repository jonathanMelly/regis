// internal/tui/rdiff.go
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"git.disroot.org/jmy/regis/internal/cue"
	"git.disroot.org/jmy/regis/internal/output"
)

// ── live-phase types ─────────────────────────────────────────────────────────

var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// liveEntry tracks the check state of one step during the live phase.
type liveEntry struct {
	info        cue.StepInfo
	done        bool
	result      cue.Result
	fileScanned int
	fileTotal   int
}

type livePrePhaseMsg    struct{ steps []cue.StepInfo }
type liveCheckResultMsg struct{ result cue.Result }
type liveFileProgressMsg struct {
	fullName      string // "scenarioLabel > cueName" (from stepWithFileProgress)
	scanned, total int
}
type liveRunCompleteMsg struct {
	results []cue.Result
	elapsed time.Duration
	err     error
}
type liveTickMsg struct{}

func liveTick() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(time.Time) tea.Msg { return liveTickMsg{} })
}


func liveProgressBar(scanned, total, width int) string {
	if total <= 0 {
		return ""
	}
	filled := scanned * width / total
	if filled > width {
		filled = width
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled) +
		fmt.Sprintf(" %d/%d", scanned, total)
}

// ── browse-mode types (unchanged) ────────────────────────────────────────────

type rdiffItemKind int

const (
	rdiffKindScenario       rdiffItemKind = iota
	rdiffKindMergedScenario               // single-cue scenario — label+cue on one line
	rdiffKindCue
	rdiffKindDetail
	rdiffKindSkipped // summary of action/service cues at the end of a scenario
)


type rdiffVisItem struct {
	kind        rdiffItemKind
	scenarioIdx int
	cueIdx      int
	line        string
}

type rdiffScenario struct {
	name        string
	label       string
	results     []cue.Result
	expanded    bool
	cueExpanded []bool
	detailLines [][]string
}

// ── model ────────────────────────────────────────────────────────────────────

type rdiffModel struct {
	// browse mode
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

	// live checking phase
	checking    bool
	liveEntries []liveEntry
	spinFrame   int

	startedAt time.Time // when the check began (for title display)
}

func (m rdiffModel) Init() tea.Cmd {
	if m.checking {
		return liveTick()
	}
	return nil
}

func (m rdiffModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	// ── live phase messages ──────────────────────────────────────────────
	case livePrePhaseMsg:
		m.liveEntries = make([]liveEntry, len(msg.steps))
		for i, s := range msg.steps {
			m.liveEntries[i] = liveEntry{info: s}
		}
		m.checking = true
		m.startedAt = time.Now()
		return m, liveTick()

	case liveCheckResultMsg:
		for i, e := range m.liveEntries {
			if e.info.ScenarioName == msg.result.ScenarioName && e.info.Name == msg.result.CueName {
				m.liveEntries[i].done = true
				m.liveEntries[i].result = msg.result
				break
			}
		}
		return m, nil

	case liveFileProgressMsg:
		// fullName = "scenarioLabel > cueName"; extract cueName.
		cueName := msg.fullName
		if idx := strings.LastIndex(msg.fullName, " > "); idx >= 0 {
			cueName = msg.fullName[idx+3:]
		}
		for i, e := range m.liveEntries {
			if !e.done && e.info.Name == cueName {
				m.liveEntries[i].fileScanned = msg.scanned
				m.liveEntries[i].fileTotal = msg.total
				break
			}
		}
		return m, nil

	case liveRunCompleteMsg:
		if len(msg.results) == 0 {
			// Fatal error before any results — just allow the user to quit.
			m.checking = false
			return m, nil
		}
		newM := newRdiffModel(msg.results, m.target, msg.elapsed, m.verbose, m.minfo)
		// Start fully expanded so the user immediately sees the diff tree.
		for i := range newM.scenarios {
			newM.scenarios[i].expanded = true
		}
		newM.visible = newM.buildVisible()
		newM.width = m.width
		newM.height = m.height
		newM.startedAt = m.startedAt
		return newM, nil

	case liveTickMsg:
		m.spinFrame = (m.spinFrame + 1) % len(spinFrames)
		if m.checking {
			return m, liveTick()
		}
		return m, nil

	// ── key events ──────────────────────────────────────────────────────
	case tea.KeyMsg:
		if m.checking {
			if msg.Type == tea.KeyCtrlC || msg.String() == "q" {
				m.quitting = true
				return m, tea.Quit
			}
			return m, nil
		}
		if m.searchMode {
			return m.updateSearch(msg)
		}
		return m.updateNormal(msg)
	}
	return m, nil
}

// ── live view ────────────────────────────────────────────────────────────────

func (m rdiffModel) rdiffTitle() string {
	ts := m.startedAt
	if ts.IsZero() {
		ts = time.Now()
	}
	return fmt.Sprintf("rdiff  %s   %s", m.target, ts.Format("02.01.2006 15:04:05"))
}

func collectWarnings(results []cue.Result) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range results {
		for _, w := range r.Warnings {
			msg := fmt.Sprintf("⚠  %s: %s", r.CueName, w)
			if !seen[msg] {
				seen[msg] = true
				out = append(out, msg)
			}
		}
	}
	return out
}

func (m rdiffModel) viewLive() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s\n\n", m.rdiffTitle())

	checked := 0
	var skippedNames []string
	seenSkipped := map[string]bool{}
	var doneResults []cue.Result
	for _, e := range m.liveEntries {
		if !e.done {
			continue
		}
		checked++
		if e.result.Status == cue.StatusSkipped && !seenSkipped[e.info.Name] {
			seenSkipped[e.info.Name] = true
			skippedNames = append(skippedNames, e.info.Name)
		}
		doneResults = append(doneResults, e.result)
	}
	warnings := collectWarnings(doneResults)

	// Reserve lines: title(1) + blank(1) + hr(1) + counter(1) + blank+skipped+warnings.
	extra := len(warnings)
	if len(skippedNames) > 0 {
		extra++
	}
	if extra > 0 {
		extra++ // blank line before skipped/warnings block
	}
	reserved := 4 + extra
	maxLines := m.height - reserved
	if maxLines < 1 {
		maxLines = 1
	}

	// Group entries by scenario, preserving order of first appearance.
	type liveGroup struct {
		label   string
		indices []int // indices into m.liveEntries
	}
	var groups []liveGroup
	groupIdx := map[string]int{}
	for i, e := range m.liveEntries {
		sn := e.info.ScenarioName
		label := e.info.ScenarioDesc
		if label == "" {
			label = sn
		}
		if idx, ok := groupIdx[sn]; ok {
			groups[idx].indices = append(groups[idx].indices, i)
		} else {
			groupIdx[sn] = len(groups)
			groups = append(groups, liveGroup{label: label, indices: []int{i}})
		}
	}

	// Render the tree — same format as viewBrowse so the transition is invisible.
	var treeLines []string
	for _, g := range groups {
		// Skip scenarios where all done entries are skipped.
		allDoneSkipped := true
		for _, i := range g.indices {
			e := m.liveEntries[i]
			if !e.done || e.result.Status != cue.StatusSkipped {
				allDoneSkipped = false
				break
			}
		}
		if allDoneSkipped {
			continue
		}

		if len(g.indices) == 1 {
			// Single-cue: merged line (mirrors rdiffKindMergedScenario).
			e := m.liveEntries[g.indices[0]]
			if e.done {
				sym := rdiffStatusSymbol(e.result)
				// Use ○ for statuses that will have expandable detail lines in browse.
				indicator := " "
				if e.result.Status == cue.StatusChanged || e.result.Status == cue.StatusFailed {
					indicator = "○"
				}
				line := fmt.Sprintf("%s %s  %s", indicator, sym, g.label)
				if e.result.CueName != e.info.ScenarioName {
					line += "   " + e.result.CueName
				}
				if e.result.FileTotal > 0 {
					line += fmt.Sprintf("  ~%d/%d", e.result.FileChanged, e.result.FileTotal)
				}
				treeLines = append(treeLines, line)
			} else {
				spin := spinFrames[m.spinFrame]
				line := fmt.Sprintf("  %s  %s", spin, g.label)
				if e.info.Name != e.info.ScenarioName {
					line += "   " + e.info.Name
				}
				if e.fileTotal > 0 {
					line += "  " + liveProgressBar(e.fileScanned, e.fileTotal, 16)
				}
				treeLines = append(treeLines, line)
			}
		} else {
			// Multi-cue: scenario header + cue lines (mirrors rdiffKindScenario + rdiffKindCue).
			allDone, changed := true, 0
			for _, i := range g.indices {
				e := m.liveEntries[i]
				if e.done && e.result.Status == cue.StatusSkipped {
					continue
				}
				if !e.done {
					allDone = false
				} else if e.result.Status == cue.StatusChanged {
					changed++
				}
			}
			var info string
			if allDone {
				if changed > 0 {
					info = fmt.Sprintf("%d changed", changed)
				} else {
					info = "all in sync"
				}
			} else {
				info = spinFrames[m.spinFrame]
			}
			treeLines = append(treeLines, fmt.Sprintf("● %s   %s", g.label, info))
			for _, i := range g.indices {
				e := m.liveEntries[i]
				if e.done && e.result.Status == cue.StatusSkipped {
					continue
				}
				if e.done {
					sym := rdiffStatusSymbol(e.result)
					line := fmt.Sprintf("    %s  %s", sym, e.info.Name)
					if e.result.FileTotal > 0 {
						line += fmt.Sprintf("  ~%d/%d", e.result.FileChanged, e.result.FileTotal)
					}
					treeLines = append(treeLines, line)
				} else {
					spin := spinFrames[m.spinFrame]
					line := fmt.Sprintf("    %s  %s", spin, e.info.Name)
					if e.fileTotal > 0 {
						line += "  " + liveProgressBar(e.fileScanned, e.fileTotal, 16)
					}
					treeLines = append(treeLines, line)
				}
			}
		}
	}
	if len(treeLines) > maxLines {
		treeLines = treeLines[:maxLines]
	}
	for _, l := range treeLines {
		sb.WriteString(l + "\n")
	}

	ruleWidth := m.width - 1
	if ruleWidth < 10 {
		ruleWidth = 49
	}
	sb.WriteString(strings.Repeat("─", ruleWidth) + "\n")
	if len(m.liveEntries) == 0 {
		fmt.Fprintf(&sb, "checking %s…\n", m.target)
	} else {
		fmt.Fprintf(&sb, "%d / %d   q quit\n", checked, len(m.liveEntries))
	}
	if len(skippedNames) > 0 || len(warnings) > 0 {
		sb.WriteString("\n")
	}
	if len(skippedNames) > 0 {
		fmt.Fprintf(&sb, "·  skipped: %s\n", strings.Join(skippedNames, ", "))
	}
	for _, w := range warnings {
		fmt.Fprintf(&sb, "%s\n", w)
	}
	return sb.String()
}

// ── browse view ──────────────────────────────────────────────────────────────

func (m rdiffModel) View() string {
	if m.quitting {
		return ""
	}
	if m.checking {
		return m.viewLive()
	}
	return m.viewBrowse()
}

func (m rdiffModel) viewBrowse() string {
	warnings := collectWarnings(m.allResults())

	seenSkipped := map[string]bool{}
	var skippedNames []string
	for _, r := range m.allResults() {
		if r.Status == cue.StatusSkipped && !seenSkipped[r.CueName] {
			seenSkipped[r.CueName] = true
			skippedNames = append(skippedNames, r.CueName)
		}
	}

	extra := len(warnings)
	if len(skippedNames) > 0 {
		extra++
	}
	if extra > 0 {
		extra++ // blank line separating stats from skipped/warnings block
	}
	reserved := 6 + extra // title + blank + rule + summary + blank + help
	if m.searchMode {
		reserved = 7 + extra
	}
	listHeight := m.height - reserved
	if listHeight < 1 {
		listHeight = 1
	}

	listLines := m.renderList()
	start, end := m.scrollWindow(listLines, listHeight)

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s\n\n", m.rdiffTitle())
	for _, l := range listLines[start:end] {
		sb.WriteString(l + "\n")
	}

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
	if len(skippedNames) > 0 || len(warnings) > 0 {
		sb.WriteString("\n")
	}
	if len(skippedNames) > 0 {
		fmt.Fprintf(&sb, "·  skipped: %s\n", strings.Join(skippedNames, ", "))
	}
	for _, w := range warnings {
		fmt.Fprintf(&sb, "%s\n", w)
	}

	if m.searchMode {
		fmt.Fprintf(&sb, "\n/ %s█\n", m.searchQuery)
	} else {
		sb.WriteString("\n↑↓ navigate  →/enter expand  ← collapse  +/- all  / search  q quit\n")
	}
	return sb.String()
}

// ── browse helpers (unchanged) ───────────────────────────────────────────────

func cueSubDetailLines(r cue.Result, verbose bool, minfo *output.ManifestInfo) []string {
	lines := output.CueDetailLines(r, true, verbose, minfo)
	if len(lines) == 0 {
		return nil
	}
	lines = lines[1:]
	result := make([]string, len(lines))
	for i, l := range lines {
		result[i] = strings.TrimPrefix(l, "    ")
	}
	return result
}

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
		startedAt: time.Now(),
	}
	m.visible = m.buildVisible()
	return m
}

func (m rdiffModel) buildVisible() []rdiffVisItem {
	var items []rdiffVisItem
	query := strings.ToLower(m.searchQuery)
	for si, sc := range m.scenarios {
		// Entirely-skipped scenarios are omitted from the tree; they appear in the footer.
		allSkipped := true
		for _, r := range sc.results {
			if r.Status != cue.StatusSkipped {
				allSkipped = false
				break
			}
		}
		if allSkipped {
			continue
		}

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

		// Single-cue scenario (non-skipped guaranteed by allSkipped check above).
		if len(sc.results) == 1 {
			ci := 0
			items = append(items, rdiffVisItem{kind: rdiffKindMergedScenario, scenarioIdx: si, cueIdx: ci})
			if sc.cueExpanded[ci] {
				for _, line := range sc.detailLines[ci] {
					items = append(items, rdiffVisItem{kind: rdiffKindDetail, scenarioIdx: si, cueIdx: ci, line: line})
				}
			}
			continue
		}

		// Multi-cue scenario: show non-skipped cues only.
		items = append(items, rdiffVisItem{kind: rdiffKindScenario, scenarioIdx: si, cueIdx: -1})
		if !sc.expanded {
			continue
		}
		for ci, r := range sc.results {
			if r.Status == cue.StatusSkipped {
				continue
			}
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
	case rdiffKindMergedScenario, rdiffKindCue:
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
	case rdiffKindMergedScenario, rdiffKindCue:
		m.scenarios[item.scenarioIdx].cueExpanded[item.cueIdx] = false
	case rdiffKindDetail:
		si, ci := item.scenarioIdx, item.cueIdx
		m.scenarios[si].cueExpanded[ci] = false
		m.visible = m.buildVisible()
		// Move cursor up to parent (cue or merged-scenario row).
		for i, v := range m.visible {
			parentKind := v.kind == rdiffKindCue || v.kind == rdiffKindMergedScenario
			if parentKind && v.scenarioIdx == si && v.cueIdx == ci {
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
		k := m.visible[i].kind
		if k == rdiffKindScenario || k == rdiffKindMergedScenario {
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
			changed, skipped := 0, 0
			for _, r := range sc.results {
				switch r.Status {
				case cue.StatusChanged:
					changed++
				case cue.StatusSkipped:
					skipped++
				}
			}
			info := "all in sync"
			if changed > 0 {
				info = fmt.Sprintf("%d changed", changed)
			} else if skipped == len(sc.results) {
				info = "skipped"
			}
			line = fmt.Sprintf("%s %s   %s", arrow, sc.label, info)
		case rdiffKindMergedScenario:
			sc := m.scenarios[item.scenarioIdx]
			r := sc.results[item.cueIdx]
			sym := rdiffStatusSymbol(r)
			// Expand indicator only when there are diff details to show.
			indicator := " "
			if len(sc.detailLines[item.cueIdx]) > 0 {
				if sc.cueExpanded[item.cueIdx] {
					indicator = "●"
				} else {
					indicator = "○"
				}
			}
			line = fmt.Sprintf("%s %s  %s", indicator, sym, sc.label)
			if r.CueName != sc.name {
				line += "   " + r.CueName
			}
			if r.FileTotal > 0 {
				line += fmt.Sprintf("  ~%d/%d", r.FileChanged, r.FileTotal)
			}
		case rdiffKindCue:
			r := m.scenarios[item.scenarioIdx].results[item.cueIdx]
			sym := rdiffStatusSymbol(r)
			name := r.CueName
			if r.FileTotal > 0 {
				name += fmt.Sprintf("  ~%d/%d", r.FileChanged, r.FileTotal)
			}
			line = fmt.Sprintf("    %s  %s", sym, name)
		case rdiffKindSkipped:
			line = "    ·  " + item.line
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

// ── entry points ─────────────────────────────────────────────────────────────

// RunRdiffTUI runs the interactive Bubble Tea TUI for rdiff (post-check, Level3 fallback).
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

// RunLiveRdiffTUI launches a live TUI immediately, shows all expected cues as
// spinning indicators while runFn executes in a goroutine, then transitions to
// the interactive browse view when checking completes.
//
// runFn is called with a context already wired for WithPrePhase, WithCheckResult,
// and WithFileProgress — do not set those callbacks before calling this function.
//
// On exit, prints the static RenderTree summary to stdout (mirrors RunRdiffTUI).
// Blocks until the user quits the browse view.
func RunLiveRdiffTUI(
	ctx context.Context,
	target string,
	verbose bool,
	level output.Level,
	minfo *output.ManifestInfo,
	runFn func(ctx context.Context) ([]cue.Result, time.Duration, error),
) error {
	m := rdiffModel{target: target, verbose: verbose, minfo: minfo, width: 80, height: 24}
	prog := tea.NewProgram(m, tea.WithAltScreen())

	ctx = cue.WithPrePhase(ctx, func(steps []cue.StepInfo) {
		prog.Send(livePrePhaseMsg{steps: steps})
	})
	ctx = cue.WithCheckResult(ctx, func(r cue.Result) {
		prog.Send(liveCheckResultMsg{result: r})
	})
	ctx = cue.WithFileProgress(ctx, func(fullName string, scanned, total int) {
		prog.Send(liveFileProgressMsg{fullName: fullName, scanned: scanned, total: total})
	})

	type runResult struct {
		results []cue.Result
		elapsed time.Duration
		err     error
	}
	ch := make(chan runResult, 1)
	go func() {
		results, elapsed, err := runFn(ctx)
		prog.Send(liveRunCompleteMsg{results: results, elapsed: elapsed, err: err})
		ch <- runResult{results, elapsed, err}
	}()

	_, tuiErr := prog.Run()
	rr := <-ch // wait for runner (may block briefly if user quit early)

	if tuiErr != nil {
		return tuiErr
	}
	if rr.results != nil {
		fmt.Print(output.RenderTree(rr.results, target, rr.elapsed, true, verbose, level, minfo))
	}
	return rr.err
}
