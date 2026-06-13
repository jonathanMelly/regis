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

// ── ANSI helpers ─────────────────────────────────────────────────────────────

const (
	ansiReset     = "\033[m"
	ansiDim       = "\033[2m"
	ansiGreen     = "\033[32m"
	ansiYellow    = "\033[33m"
	ansiRed       = "\033[31m"
	ansiDimYellow = "\033[2;33m"
	ansiReverse   = "\033[7m"
	ansiBold      = "\033[1m"
)

func colorize(s, color string) string { return color + s + ansiReset }

// ── live-phase types ─────────────────────────────────────────────────────────

var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

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
	fullName      string
	scanned, total int
}
type liveRunCompleteMsg struct {
	results   []cue.Result
	elapsed   time.Duration
	err       error
	confirmCh chan bool // non-nil after phase-1 check: TUI waits for user to press 'r'
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

// ── browse-mode types ─────────────────────────────────────────────────────────

// tabID identifies which tab is active for a cue row.
type tabID int

const (
	tabDetails tabID = 0
	tabExec    tabID = 1
	tabCount   tabID = 2
)

type rdiffItemKind int

const (
	rdiffKindScenario       rdiffItemKind = iota
	rdiffKindMergedScenario               // single-cue scenario — label+cue on one line
	rdiffKindCue
)

type rdiffVisItem struct {
	kind        rdiffItemKind
	scenarioIdx int
	cueIdx      int
}

type rdiffScenario struct {
	name        string
	label       string
	results     []cue.Result
	expanded    bool
	cueExpanded []bool     // whether tab content is visible
	activeTab   []tabID    // which tab is active
	detailLines [][]string // per-cue: CueInfoLines output (renamed details)
	execLines   [][]string // per-cue: CueExecLines output
}

// ── model ────────────────────────────────────────────────────────────────────

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
	isRun       bool
	hideSkipped bool

	// live phase
	checking    bool
	liveEntries []liveEntry
	spinFrame   int
	startedAt   time.Time

	// deploy gate (isRun=true, two-phase)
	confirmCh chan bool // non-nil when waiting for user to press 'r' before deploying
	confirming bool    // true when showing the "run? [y/N]" confirmation line
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
			m.checking = false
			if msg.confirmCh != nil {
				select {
				case msg.confirmCh <- false:
				default:
				}
			}
			return m, nil
		}
		newM := newLiveModel(msg.results, m.target, msg.elapsed, m.verbose, m.minfo, m.isRun)
		for i := range newM.scenarios {
			newM.scenarios[i].expanded = true
		}
		newM.visible = newM.buildVisible()
		newM.width = m.width
		newM.height = m.height
		newM.startedAt = m.startedAt
		newM.confirmCh = msg.confirmCh
		return newM, nil

	case liveTickMsg:
		m.spinFrame = (m.spinFrame + 1) % len(spinFrames)
		if m.checking {
			return m, liveTick()
		}
		return m, nil

	case tea.KeyMsg:
		if m.checking {
			if msg.Type == tea.KeyCtrlC || msg.String() == "q" {
				m.quitting = true
				return m, tea.Quit
			}
			return m, nil
		}
		if m.confirming {
			return m.updateConfirm(msg)
		}
		if m.searchMode {
			return m.updateSearch(msg)
		}
		return m.updateNormal(msg)
	}
	return m, nil
}

// ── live view ─────────────────────────────────────────────────────────────────

func (m rdiffModel) titleLine() string {
	ts := m.startedAt
	if ts.IsZero() {
		ts = time.Now()
	}
	verb := "rdiff"
	if m.isRun {
		verb = "run  "
	}
	return fmt.Sprintf("%s  %s   %s", verb, m.target, ts.Format("02.01.2006 15:04:05"))
}

func liveSymbol(r cue.Result, spinFrame int, done bool) string {
	if !done {
		return colorize(spinFrames[spinFrame], ansiYellow)
	}
	switch r.Status {
	case cue.StatusChanged:
		if !r.LocalMtime.IsZero() && !r.RemoteMtime.IsZero() && r.RemoteMtime.After(r.LocalMtime) {
			return colorize("↓", ansiGreen)
		}
		return colorize("↑", ansiGreen)
	case cue.StatusEqual:
		return colorize("=", ansiDim)
	case cue.StatusFailed:
		return colorize("✕", ansiRed)
	case cue.StatusSkipped:
		return colorize("-", ansiDimYellow)
	}
	return "?"
}

func (m rdiffModel) viewLive() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s\n\n", m.titleLine())

	checked := 0
	for _, e := range m.liveEntries {
		if e.done {
			checked++
		}
	}

	reserved := 4
	maxLines := m.height - reserved
	if maxLines < 1 {
		maxLines = 1
	}

	type liveGroup struct {
		label   string
		indices []int
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

	var treeLines []string
	for _, g := range groups {
		if len(g.indices) == 1 {
			e := m.liveEntries[g.indices[0]]
			sym := liveSymbol(e.result, m.spinFrame, e.done)
			name := e.info.Name
			if e.done {
				name = e.result.CueName
			}
			line := fmt.Sprintf("  %s  %s", sym, g.label)
			if name != e.info.ScenarioName {
				line += "   " + name
			}
			if e.done && e.result.FileTotal > 0 {
				line += fmt.Sprintf("  ~%d/%d", e.result.FileChanged, e.result.FileTotal)
			} else if !e.done && e.fileTotal > 0 {
				line += "  " + liveProgressBar(e.fileScanned, e.fileTotal, 16)
			}
			if e.done && len(e.result.Warnings) > 0 {
				line += "  ⚠"
			}
			treeLines = append(treeLines, line)
		} else {
			allDone, changed := true, 0
			for _, i := range g.indices {
				e := m.liveEntries[i]
				if !e.done {
					allDone = false
				} else if e.result.Status == cue.StatusChanged {
					changed++
				}
			}
			var info string
			if allDone {
				if changed > 0 {
					info = colorize(fmt.Sprintf("%d changed", changed), ansiGreen)
				} else {
					info = colorize("all in sync", ansiDim)
				}
			} else {
				info = colorize(spinFrames[m.spinFrame], ansiYellow)
			}
			treeLines = append(treeLines, fmt.Sprintf("● %s   %s", g.label, info))
			for _, i := range g.indices {
				e := m.liveEntries[i]
				sym := liveSymbol(e.result, m.spinFrame, e.done)
				name := e.info.Name
				if e.done {
					name = e.result.CueName
				}
				line := fmt.Sprintf("    %s  %s", sym, name)
				if e.done && e.result.FileTotal > 0 {
					line += fmt.Sprintf("  ~%d/%d", e.result.FileChanged, e.result.FileTotal)
				} else if !e.done && e.fileTotal > 0 {
					line += "  " + liveProgressBar(e.fileScanned, e.fileTotal, 16)
				}
				if e.done && len(e.result.Warnings) > 0 {
					line += "  ⚠"
				}
				treeLines = append(treeLines, line)
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
		verb := "checking"
		if m.isRun {
			verb = "deploying"
		}
		fmt.Fprintf(&sb, "%s %s…\n", verb, m.target)
	} else {
		fmt.Fprintf(&sb, "%d / %d   q quit\n", checked, len(m.liveEntries))
	}
	return sb.String()
}

// ── browse view ───────────────────────────────────────────────────────────────

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
	reserved := 6
	if m.searchMode {
		reserved = 7
	}
	listHeight := m.height - reserved
	if listHeight < 1 {
		listHeight = 1
	}

	listLines, cursorLine := m.renderListAndCursorLine()
	start, end := scrollWindowAt(listLines, listHeight, cursorLine)

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s\n\n", m.titleLine())
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

	if m.searchMode {
		fmt.Fprintf(&sb, "\n/ %s█\n", m.searchQuery)
	} else if m.confirming {
		fmt.Fprintf(&sb, "\nrun? [y/N]\n")
	} else {
		var skippedHint string
		if m.hideSkipped {
			n := m.countSkipped()
			if n > 0 {
				skippedHint = fmt.Sprintf("  h show-skipped (%d)", n)
			} else {
				skippedHint = "  h show-skipped"
			}
		} else {
			skippedHint = "  h hide-skipped"
		}
		var runHint string
		if m.confirmCh != nil {
			runHint = "  r run"
		}
		fmt.Fprintf(&sb, "\n↑↓ navigate  →← tab/collapse  +/- all  / search%s%s  q quit\n", skippedHint, runHint)
	}
	return sb.String()
}

// ── browse helpers ────────────────────────────────────────────────────────────

func browseSymbol(r cue.Result) string {
	switch r.Status {
	case cue.StatusChanged:
		if !r.LocalMtime.IsZero() && !r.RemoteMtime.IsZero() && r.RemoteMtime.After(r.LocalMtime) {
			return colorize("↓", ansiGreen)
		}
		return colorize("↑", ansiGreen)
	case cue.StatusEqual:
		return colorize("=", ansiDim)
	case cue.StatusFailed:
		return colorize("✕", ansiRed)
	case cue.StatusSkipped:
		return colorize("-", ansiDimYellow)
	}
	return "?"
}

// tabBar builds the inline tab bar string for a cue with tab content.
// Returns "" when neither tab has content (no bar shown).
func tabBar(sc rdiffScenario, ci int, isCursor bool) string {
	hasDetails := len(sc.detailLines[ci]) > 0
	hasExec := len(sc.execLines[ci]) > 0
	if !hasDetails && !hasExec {
		return ""
	}
	active := sc.activeTab[ci]

	renderTab := func(name string, t tabID) string {
		if t == active {
			// Active tab: bold + brackets
			label := "[" + name + "]"
			if isCursor {
				return ansiBold + label + ansiReset + ansiReverse
			}
			return ansiBold + label + ansiReset
		}
		// Inactive but available: dim
		if (t == tabDetails && hasDetails) || (t == tabExec && hasExec) {
			return colorize(name, ansiDim)
		}
		return ""
	}

	parts := []string{}
	if hasDetails || active == tabDetails {
		parts = append(parts, renderTab("details", tabDetails))
	}
	if hasExec || active == tabExec {
		parts = append(parts, renderTab("exec", tabExec))
	}
	// Filter empty
	var filtered []string
	for _, p := range parts {
		if p != "" {
			filtered = append(filtered, p)
		}
	}
	if len(filtered) == 0 {
		return ""
	}
	return "  " + strings.Join(filtered, "  ")
}

// activeTabLines returns the content lines for the currently active tab of a cue.
func activeTabLines(sc rdiffScenario, ci int) []string {
	switch sc.activeTab[ci] {
	case tabDetails:
		return sc.detailLines[ci]
	case tabExec:
		return sc.execLines[ci]
	}
	return nil
}

func newLiveModel(
	results []cue.Result,
	target string,
	total time.Duration,
	verbose bool,
	minfo *output.ManifestInfo,
	isRun bool,
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
		n := len(g.rows)
		detailL := make([][]string, n)
		execL := make([][]string, n)
		for j, r := range g.rows {
			detailL[j] = output.CueInfoLines(r, true, minfo)
			execL[j] = output.CueExecLines(r, verbose)
		}
		scenarios[i] = rdiffScenario{
			name:        g.name,
			label:       g.label,
			results:     g.rows,
			expanded:    false,
			cueExpanded: make([]bool, n),
			activeTab:   make([]tabID, n),
			detailLines: detailL,
			execLines:   execL,
		}
	}

	m := rdiffModel{
		scenarios: scenarios,
		target:    target,
		total:     total,
		verbose:   verbose,
		minfo:     minfo,
		isRun:     isRun,
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

		if len(sc.results) == 1 {
			ci := 0
			r := sc.results[ci]
			if m.hideSkipped && r.Status == cue.StatusSkipped {
				continue
			}
			items = append(items, rdiffVisItem{kind: rdiffKindMergedScenario, scenarioIdx: si, cueIdx: ci})
			continue
		}

		// Multi-cue: check if any non-skipped cue would show.
		hasVisible := false
		for _, r := range sc.results {
			if !m.hideSkipped || r.Status != cue.StatusSkipped {
				hasVisible = true
				break
			}
		}
		if !hasVisible {
			continue
		}

		items = append(items, rdiffVisItem{kind: rdiffKindScenario, scenarioIdx: si, cueIdx: -1})
		if !sc.expanded {
			continue
		}
		for ci, r := range sc.results {
			if m.hideSkipped && r.Status == cue.StatusSkipped {
				continue
			}
			if query != "" && !scenarioMatch && !strings.Contains(strings.ToLower(r.CueName), query) {
				continue
			}
			items = append(items, rdiffVisItem{kind: rdiffKindCue, scenarioIdx: si, cueIdx: ci})
		}
	}
	return items
}

// renderListAndCursorLine renders all visible items to a flat line slice.
// cursorLine is the index of the first line belonging to the cursor item.
func (m rdiffModel) renderListAndCursorLine() (lines []string, cursorLine int) {
	cursorLine = 0
	for vi, item := range m.visible {
		isCursor := vi == m.cursor
		if isCursor {
			cursorLine = len(lines)
		}
		itemLines := m.renderItem(item, isCursor)
		lines = append(lines, itemLines...)
	}
	return
}

// renderItem returns the display lines for a single visible item.
// An expanded cue row includes its active tab content lines beneath it.
func (m rdiffModel) renderItem(item rdiffVisItem, isCursor bool) []string {
	si, ci := item.scenarioIdx, item.cueIdx

	var header string
	switch item.kind {
	case rdiffKindScenario:
		sc := m.scenarios[si]
		arrow := "○"
		if sc.expanded {
			arrow = "●"
		}
		changed, failed := 0, 0
		for _, r := range sc.results {
			switch r.Status {
			case cue.StatusChanged:
				changed++
			case cue.StatusFailed:
				failed++
			}
		}
		var info string
		switch {
		case failed > 0:
			info = colorize(fmt.Sprintf("%d failed", failed), ansiRed)
		case changed > 0:
			info = colorize(fmt.Sprintf("%d changed", changed), ansiGreen)
		default:
			info = colorize("all in sync", ansiDim)
		}
		header = fmt.Sprintf("%s %s   %s", arrow, sc.label, info)

	case rdiffKindMergedScenario:
		sc := m.scenarios[si]
		r := sc.results[ci]
		sym := browseSymbol(r)
		hasContent := len(sc.detailLines[ci]) > 0 || len(sc.execLines[ci]) > 0
		indicator := " "
		if hasContent {
			if sc.cueExpanded[ci] {
				indicator = "●"
			} else {
				indicator = "○"
			}
		}
		header = fmt.Sprintf("%s %s  %s", indicator, sym, sc.label)
		if r.CueName != sc.name {
			header += "   " + r.CueName
		}
		if r.FileTotal > 0 {
			header += fmt.Sprintf("  ~%d/%d", r.FileChanged, r.FileTotal)
		}
		if len(r.Warnings) > 0 {
			header += "  ⚠"
		}
		if sc.cueExpanded[ci] {
			header += tabBar(sc, ci, isCursor)
		}

	case rdiffKindCue:
		sc := m.scenarios[si]
		r := sc.results[ci]
		sym := browseSymbol(r)
		hasContent := len(sc.detailLines[ci]) > 0 || len(sc.execLines[ci]) > 0
		indicator := " "
		if hasContent {
			if sc.cueExpanded[ci] {
				indicator = "●"
			} else {
				indicator = "○"
			}
		}
		name := r.CueName
		if r.FileTotal > 0 {
			name += fmt.Sprintf("  ~%d/%d", r.FileChanged, r.FileTotal)
		}
		header = fmt.Sprintf("  %s %s  %s", indicator, sym, name)
		if len(r.Warnings) > 0 {
			header += "  ⚠"
		}
		if sc.cueExpanded[ci] {
			header += tabBar(sc, ci, isCursor)
		}
	}

	if isCursor {
		header = ansiReverse + header + ansiReset
	}

	lines := []string{header}

	// Append active tab content lines when expanded.
	if item.kind != rdiffKindScenario {
		sc := m.scenarios[si]
		if sc.cueExpanded[ci] {
			for _, l := range activeTabLines(sc, ci) {
				lines = append(lines, "      "+l)
			}
		}
	}
	return lines
}

func scrollWindowAt(lines []string, height, cursorLine int) (start, end int) {
	n := len(lines)
	if n <= height {
		return 0, n
	}
	start = cursorLine - height/2
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

// updateConfirm handles keypresses while the "run? [y/N]" prompt is shown.
func (m rdiffModel) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.String() == "y" || msg.String() == "Y":
		select {
		case m.confirmCh <- true:
		default:
		}
		m.confirmCh = nil
		m.confirming = false
		m.checking = true
		m.liveEntries = nil
		m.startedAt = time.Now()
		return m, liveTick()
	case msg.Type == tea.KeyCtrlC:
		select {
		case m.confirmCh <- false:
		default:
		}
		m.quitting = true
		return m, tea.Quit
	default: // Enter, n, N, Escape, any other key → cancel
		m.confirming = false
		return m, nil
	}
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
		m = m.actionRight()
	case tea.KeyLeft, tea.KeyBackspace:
		m = m.actionLeft()
	case tea.KeyShiftTab:
		m = m.jumpPrevScenario()
	case tea.KeyCtrlC:
		if m.confirmCh != nil {
			select {
			case m.confirmCh <- false:
			default:
			}
		}
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
		case "h":
			m.hideSkipped = !m.hideSkipped
			m.visible = m.buildVisible()
			m.cursor = rdiffClamp(m.cursor, 0, len(m.visible)-1)
		case "/":
			m.searchMode = true
			m.searchQuery = ""
		case "r":
			if m.confirmCh != nil {
				m.confirming = true
			}
		case "q":
			if m.confirmCh != nil {
				select {
				case m.confirmCh <- false:
				default:
				}
			}
			m.quitting = true
			return m, tea.Quit
		}
	}
	return m, nil
}

// actionRight handles → / enter / tab:
// - Scenario: toggle expand
// - Cue collapsed: expand, set first tab
// - Cue expanded: next tab (cycles)
func (m rdiffModel) actionRight() rdiffModel {
	if len(m.visible) == 0 {
		return m
	}
	item := m.visible[m.cursor]
	si, ci := item.scenarioIdx, item.cueIdx

	switch item.kind {
	case rdiffKindScenario:
		m.scenarios[si].expanded = !m.scenarios[si].expanded

	case rdiffKindMergedScenario, rdiffKindCue:
		sc := m.scenarios[si]
		hasDetails := len(sc.detailLines[ci]) > 0
		hasExec := len(sc.execLines[ci]) > 0
		if !hasDetails && !hasExec {
			break // nothing to show
		}
		if !sc.cueExpanded[ci] {
			// Expand: open on first available tab.
			m.scenarios[si].cueExpanded[ci] = true
			if hasDetails {
				m.scenarios[si].activeTab[ci] = tabDetails
			} else {
				m.scenarios[si].activeTab[ci] = tabExec
			}
		} else {
			// Already open: cycle to next available tab.
			cur := sc.activeTab[ci]
			for delta := tabID(1); delta < tabCount; delta++ {
				next := (cur + delta) % tabCount
				if next == tabDetails && hasDetails {
					m.scenarios[si].activeTab[ci] = next
					goto done
				}
				if next == tabExec && hasExec {
					m.scenarios[si].activeTab[ci] = next
					goto done
				}
			}
		}
	}
done:
	m.visible = m.buildVisible()
	m.cursor = rdiffClamp(m.cursor, 0, len(m.visible)-1)
	return m
}

// actionLeft handles ←:
// - Scenario: collapse
// - Cue expanded at tab > 0: go to previous tab
// - Cue expanded at tab 0: collapse
// - Cue collapsed: collapse parent scenario
func (m rdiffModel) actionLeft() rdiffModel {
	if len(m.visible) == 0 {
		return m
	}
	item := m.visible[m.cursor]
	si, ci := item.scenarioIdx, item.cueIdx

	switch item.kind {
	case rdiffKindScenario:
		m.scenarios[si].expanded = false

	case rdiffKindMergedScenario, rdiffKindCue:
		sc := m.scenarios[si]
		if sc.cueExpanded[ci] {
			cur := sc.activeTab[ci]
			// Search strictly backwards for a previous available tab (no wrap).
			found := false
			for t := int(cur) - 1; t >= 0; t-- {
				if tabID(t) == tabDetails && len(sc.detailLines[ci]) > 0 {
					m.scenarios[si].activeTab[ci] = tabID(t)
					found = true
					break
				}
				if tabID(t) == tabExec && len(sc.execLines[ci]) > 0 {
					m.scenarios[si].activeTab[ci] = tabID(t)
					found = true
					break
				}
			}
			if !found {
				// Already at the first available tab: collapse.
				m.scenarios[si].cueExpanded[ci] = false
			}
		} else if item.kind == rdiffKindCue {
			// Collapsed cue: collapse the parent scenario.
			m.scenarios[si].expanded = false
		}
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
			sc := m.scenarios[i]
			hasContent := len(sc.detailLines[j]) > 0 || len(sc.execLines[j]) > 0
			m.scenarios[i].cueExpanded[j] = hasContent
			if hasContent {
				if len(sc.detailLines[j]) > 0 {
					m.scenarios[i].activeTab[j] = tabDetails
				} else {
					m.scenarios[i].activeTab[j] = tabExec
				}
			}
		}
	}
	m.visible = m.buildVisible()
	m.cursor = rdiffClamp(m.cursor, 0, len(m.visible)-1)
	return m
}

func (m rdiffModel) allResults() []cue.Result {
	var all []cue.Result
	for _, sc := range m.scenarios {
		all = append(all, sc.results...)
	}
	return all
}

func (m rdiffModel) countSkipped() int {
	n := 0
	for _, sc := range m.scenarios {
		for _, r := range sc.results {
			if r.Status == cue.StatusSkipped {
				n++
			}
		}
	}
	return n
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

// ── entry points ──────────────────────────────────────────────────────────────

// RunLiveTUI launches a live TUI that runs phase1Fn immediately, then transitions to
// browse mode. If phase2Fn is non-nil the browse footer shows "r run"; pressing 'r'
// triggers a confirmation then runs phase2Fn (also showing a live view).
//
// Typical usage:
//   - rdiff:              phase1Fn=check, phase2Fn=nil
//   - run (normal):       phase1Fn=check, phase2Fn=run
//   - run --run-without-check: phase1Fn=run,   phase2Fn=nil  (skip rdiff phase entirely)
func RunLiveTUI(
	ctx context.Context,
	target string,
	isRun bool,
	verbose bool,
	level output.Level,
	minfo *output.ManifestInfo,
	phase1Fn func(ctx context.Context) ([]cue.Result, time.Duration, error),
	phase2Fn func(ctx context.Context) ([]cue.Result, time.Duration, error),
) error {
	var confirmCh chan bool
	if phase2Fn != nil {
		confirmCh = make(chan bool, 1)
	}

	m := rdiffModel{
		target:    target,
		verbose:   verbose,
		minfo:     minfo,
		isRun:     isRun,
		width:     80,
		height:    24,
		confirmCh: confirmCh,
	}
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
		// Phase 1.
		results, elapsed, err := phase1Fn(ctx)

		var msgConfirmCh chan bool
		if phase2Fn != nil && err == nil {
			msgConfirmCh = confirmCh
		}
		prog.Send(liveRunCompleteMsg{results: results, elapsed: elapsed, err: err, confirmCh: msgConfirmCh})

		if phase2Fn == nil {
			ch <- runResult{results, elapsed, err}
			return
		}

		// Wait for user to press 'r' (true) or quit (false).
		if confirmed := <-confirmCh; !confirmed {
			ch <- runResult{results, elapsed, nil}
			return
		}

		// Phase 2. Reuses ctx with live callbacks so the runner's internal pre-check
		// fires livePrePhaseMsg/liveCheckResultMsg again for a fresh live view.
		r2, e2, err2 := phase2Fn(ctx)
		prog.Send(liveRunCompleteMsg{results: r2, elapsed: e2, err: err2, confirmCh: nil})
		ch <- runResult{r2, e2, err2}
	}()

	_, tuiErr := prog.Run()

	// If the TUI exited before the user confirmed, unblock the goroutine.
	if confirmCh != nil {
		select {
		case confirmCh <- false:
		default:
		}
	}

	rr := <-ch

	if tuiErr != nil {
		return tuiErr
	}
	if rr.results != nil {
		fmt.Print(output.RenderTree(rr.results, target, rr.elapsed, true, verbose, level, minfo))
	}
	return rr.err
}

