// internal/tui/live.go
package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"git.disroot.org/jmy/regis/internal/cue"
	"git.disroot.org/jmy/regis/internal/output"
	"git.disroot.org/jmy/regis/internal/runner"
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
	currentFile string // filename being uploaded (from liveFileProgressMsg)
	// apply phase
	applying    bool       // stage 2 is executing this step right now
	applyDone   bool       // stage 2 result received
	applyResult cue.Result
	outputLines []string   // ring buffer of last 8 output lines
	pinnedOpen  bool       // user pressed space → keeps output visible after done
}

type livePrePhaseMsg    struct{ steps []cue.StepInfo }
type liveCheckResultMsg struct{ result cue.Result }
type liveFileProgressMsg struct {
	fullName      string
	scanned, total int
}
type liveApplyStepMsg struct {
	scenarioName string
	cueName      string
}
type liveApplyResultMsg struct{ result cue.Result }
type liveOutputLineMsg struct {
	scenarioName string
	cueName      string
	line         string
	isStderr     bool
}

// PhaseFunc is an alias for runner.PhaseFunc kept for backward compatibility.
// Use runner.PhaseFunc directly in new code.
type PhaseFunc = runner.PhaseFunc

// confirmDecision is sent on the confirm channel when the user proceeds or cancels.
type confirmDecision struct {
	proceed         bool
	overrideOnError string // "compensate" | "halt"
}

func phaseGerund(label string) string {
	switch label {
	case "run":
		return "running"
	default:
		return label + "ing"
	}
}

type liveRunCompleteMsg struct {
	results        []cue.Result
	elapsed        time.Duration
	err            error
	confirmCh      chan confirmDecision // non-nil when a next phase is available
	phaseLabel     string              // label of the phase that just completed
	nextPhaseLabel string              // label of the upcoming phase (empty if none)
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

type phaseItemKind int

const (
	phaseKindScenario       phaseItemKind = iota
	phaseKindMergedScenario               // single-cue scenario — label+cue on one line
	phaseKindCue
)

type phaseVisItem struct {
	kind        phaseItemKind
	scenarioIdx int
	cueIdx      int
}

type phaseScenario struct {
	name             string
	label            string
	results          []cue.Result
	expanded         bool
	cueExpanded      []bool     // whether tab content is visible
	activeTab        []tabID    // which tab is active
	detailLines      [][]string // per-cue: CueInfoLines output (renamed details)
	execLines        [][]string // per-cue: CueExecLines output
	cueContentOffset []int      // per-cue scroll offset within active tab content
}

// ── model ────────────────────────────────────────────────────────────────────

type phaseModel struct {
	scenarios   []phaseScenario
	visible     []phaseVisItem
	cursor      int
	searchMode  bool
	searchQuery string
	target      string
	total       time.Duration
	verbose     bool
	minfo       *output.ManifestInfo
	width       int
	height      int
	quitting       bool
	phaseLabel     string
	nextPhaseLabel string
	hideSkipped    bool

	gitSHA string // short hash shown in check phase title

	// live phase
	checking         bool
	liveEntries      []liveEntry
	spinFrame        int
	startedAt        time.Time
	currentApplyDesc string // "scenario / cue  →  cmd" shown during apply stage
	liveScroll       int    // scroll offset into treeLines
	liveCursor       int    // -1 = auto-follow, >=0 = user-selected entry index
	liveAutoScroll   bool   // true once auto-scroll is active (apply phase started)

	// browse content-scroll
	contentFocus bool // true = ↑/↓ scrolls item tab content, not the list

	// deploy gate (isRun=true, two-phase)
	confirmCh chan confirmDecision // non-nil when waiting for user to press 'r' before deploying
	confirming bool               // true when showing the "run? [y/N]" confirmation line
	errMsg     string             // non-empty when a phase failed with no results (shown in footer)

	// on_error compensate toggle (only active when confirmCh != nil)
	compensate         bool // current toggle state (true = compensate, false = halt)
	compensateInferred bool // initial inferred value before any user toggle
	compensateToggled  bool // user has explicitly changed the toggle

	// runBlockMsg is set when the run phase cannot start (e.g. dirty git tree).
	// It is shown in the footer in place of the normal "r run" hint.
	runBlockMsg string
}

func (m phaseModel) Init() tea.Cmd {
	if m.checking {
		return liveTick()
	}
	return nil
}

func (m phaseModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		m.liveCursor = -1
		m.liveScroll = 0
		m.liveAutoScroll = false
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
		filePart := ""
		if idx := strings.LastIndex(msg.fullName, " > "); idx >= 0 {
			filePart = msg.fullName[:idx]
			cueName = msg.fullName[idx+3:]
		}
		for i, e := range m.liveEntries {
			if !e.done && e.info.Name == cueName {
				m.liveEntries[i].fileScanned = msg.scanned
				m.liveEntries[i].fileTotal = msg.total
				m.liveEntries[i].currentFile = filePart
				break
			}
		}
		return m, nil

	case liveOutputLineMsg:
		for i, e := range m.liveEntries {
			if e.info.ScenarioName == msg.scenarioName && e.info.Name == msg.cueName {
				const maxLines = 8
				m.liveEntries[i].outputLines = append(m.liveEntries[i].outputLines, msg.line)
				if len(m.liveEntries[i].outputLines) > maxLines {
					m.liveEntries[i].outputLines = m.liveEntries[i].outputLines[len(m.liveEntries[i].outputLines)-maxLines:]
				}
				break
			}
		}
		return m, nil

	case liveApplyStepMsg:
		for i, e := range m.liveEntries {
			if e.info.ScenarioName == msg.scenarioName && e.info.Name == msg.cueName {
				m.liveEntries[i].applying = true
				// Build the command zone description from the pre-check result.
				desc := e.info.ScenarioDesc
				if desc == "" {
					desc = e.info.ScenarioName
				}
				cmdLine := e.result.Cmd
				if cmdLine == "" && e.result.Nature == "binary" && e.result.LocalPath != "" {
					cmdLine = e.result.LocalPath + " → " + e.result.RemotePath
				}
				if cmdLine != "" {
					m.currentApplyDesc = desc + " / " + msg.cueName + "   " + cmdLine
				} else {
					m.currentApplyDesc = desc + " / " + msg.cueName
				}
				// Auto-scroll: if user hasn't manually moved cursor, follow this entry.
				if m.liveCursor < 0 {
					m.liveScroll = i
				}
				m.liveAutoScroll = true
				break
			}
		}
		return m, nil

	case liveApplyResultMsg:
		for i, e := range m.liveEntries {
			if e.info.ScenarioName == msg.result.ScenarioName && e.info.Name == msg.result.CueName {
				m.liveEntries[i].applying = false
				m.liveEntries[i].applyDone = true
				m.liveEntries[i].applyResult = msg.result
				m.currentApplyDesc = ""
				break
			}
		}
		return m, nil

	case liveRunCompleteMsg:
		if len(msg.results) == 0 {
			m.checking = false
			if msg.err != nil {
				m.errMsg = msg.err.Error()
			}
			if msg.confirmCh != nil {
				select {
				case msg.confirmCh <- confirmDecision{proceed: false}:
				default:
				}
			}
			return m, nil
		}
		newM := newPhaseModel(msg.results, m.target, msg.elapsed, m.verbose, m.minfo, msg.phaseLabel, msg.nextPhaseLabel)
		for i := range newM.scenarios {
			newM.scenarios[i].expanded = true
		}
		newM.visible = newM.buildVisible()
		newM.width = m.width
		newM.height = m.height
		newM.startedAt = m.startedAt
		newM.confirmCh = msg.confirmCh
		newM.compensate = m.compensate
		newM.compensateInferred = m.compensateInferred
		newM.compensateToggled = m.compensateToggled
		newM.runBlockMsg = m.runBlockMsg
		return newM, nil

	case liveTickMsg:
		m.spinFrame = (m.spinFrame + 1) % len(spinFrames)
		if m.checking {
			return m, liveTick()
		}
		return m, nil

	case tea.KeyMsg:
		if m.checking {
			switch {
			case msg.Type == tea.KeyCtrlC || msg.String() == "q":
				m.quitting = true
				return m, tea.Quit
			case msg.Type == tea.KeyUp:
				if m.liveCursor < 0 {
					m.liveCursor = m.liveScroll
				}
				if m.liveCursor > 0 {
					m.liveCursor--
					m.liveScroll = m.liveCursor
				}
			case msg.Type == tea.KeyDown:
				if m.liveCursor < 0 {
					m.liveCursor = m.liveScroll
				}
				if m.liveCursor < len(m.liveEntries)-1 {
					m.liveCursor++
					m.liveScroll = m.liveCursor
				}
			case msg.String() == " ":
				if m.liveCursor >= 0 && m.liveCursor < len(m.liveEntries) {
					m.liveEntries[m.liveCursor].pinnedOpen = !m.liveEntries[m.liveCursor].pinnedOpen
				}
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

func (m phaseModel) titleLine() string {
	ts := m.startedAt
	if ts.IsZero() {
		ts = time.Now()
	}
	// Browse mode: show completed phase as [✓] and next phase as [ ] if pending.
	checkMark := colorize("[✓]", ansiGreen)
	phaseStr := m.phaseLabel + " " + checkMark
	if m.nextPhaseLabel != "" {
		pendingMark := colorize("[ ]", ansiDim)
		phaseStr += "  →  " + m.nextPhaseLabel + " " + pendingMark
	}
	line := phaseStr + "   " + m.target + "   " + ts.Format("02.01.2006 15:04:05")
	if m.phaseLabel == "check" && m.gitSHA != "" {
		line += "   " + m.gitSHA
	}
	return line
}

func liveSymbol(e liveEntry, spinFrame int) string {
	// Apply phase overrides: if currently applying → spinner; if apply done → apply result.
	if e.applying {
		return colorize(spinFrames[spinFrame], ansiYellow)
	}
	if e.applyDone {
		return resultSymbol(e.applyResult)
	}
	// Pre-check phase.
	if !e.done {
		return colorize(spinFrames[spinFrame], ansiYellow)
	}
	return resultSymbol(e.result)
}

func resultSymbol(r cue.Result) string {
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

// phaseStrip renders the "check [✓] → run [●]" header.
func (m phaseModel) phaseStrip() string {
	var parts []string
	// Completed phase (if nextPhaseLabel is current phase, then phaseLabel was previous).
	if m.nextPhaseLabel != "" && m.phaseLabel != "" {
		// We're in check phase showing next label; check is the current.
	}
	renderPhase := func(label string, done bool, active bool) string {
		var bracket string
		switch {
		case done:
			bracket = colorize("[✓]", ansiGreen)
		case active:
			bracket = colorize("["+spinFrames[m.spinFrame]+"]", ansiYellow)
		default:
			bracket = colorize("[ ]", ansiDim)
		}
		return label + " " + bracket
	}
	// During live check phase: phaseLabel=current, nextPhaseLabel=upcoming.
	parts = append(parts, renderPhase(m.phaseLabel, false, true))
	if m.nextPhaseLabel != "" {
		parts = append(parts, renderPhase(m.nextPhaseLabel, false, false))
	}
	ts := m.startedAt
	if ts.IsZero() {
		ts = time.Now()
	}
	line := strings.Join(parts, "  →  ")
	line += "   " + m.target + "   " + ts.Format("15:04:05")
	if m.phaseLabel == "check" && m.gitSHA != "" {
		line += "   " + m.gitSHA
	}
	return line
}

func (m phaseModel) viewLive() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s\n\n", m.phaseStrip())

	checked := 0
	for _, e := range m.liveEntries {
		if e.done {
			checked++
		}
	}

	type liveGroup struct {
		label   string
		indices []int
	}
	var groups []liveGroup
	groupIdx := map[string]int{}
	for i, e := range m.liveEntries {
		key := e.info.ScenarioName
		label := e.info.ScenarioDesc
		if label == "" {
			label = key
		}
		if idx, ok := groupIdx[key]; ok {
			groups[idx].indices = append(groups[idx].indices, i)
		} else {
			groupIdx[key] = len(groups)
			groups = append(groups, liveGroup{label: label, indices: []int{i}})
		}
	}

	// Build flat line list; each entry may produce multiple lines (output tail).
	type treeLine struct {
		entryIdx int // which liveEntry owns this line (-1 = group header)
		text     string
	}
	var treeLines []treeLine

	maxWidth := m.width - 8
	if maxWidth < 20 {
		maxWidth = 20
	}

	appendOutputLines := func(e liveEntry) {
		showOutput := e.applying || e.pinnedOpen || (e.applyDone && e.applyResult.Status == cue.StatusFailed)
		if !showOutput {
			return
		}
		tail := e.outputLines
		if len(tail) > 5 {
			tail = tail[len(tail)-5:]
		}
		for _, l := range tail {
			if len([]rune(l)) > maxWidth {
				runes := []rune(l)
				l = string(runes[:maxWidth]) + "…"
			}
			treeLines = append(treeLines, treeLine{entryIdx: -1, text: colorize("      "+l, ansiDim)})
		}
	}

	for _, g := range groups {
		if len(g.indices) == 1 {
			i := g.indices[0]
			e := m.liveEntries[i]
			sym := liveSymbol(e, m.spinFrame)
			name := e.info.Name
			if e.done {
				name = e.result.CueName
			}
			line := fmt.Sprintf("  %s  %s", sym, g.label)
			if name != e.info.ScenarioName {
				line += "   " + name
			}
			activeResult := e.result
			if e.applyDone {
				activeResult = e.applyResult
			}
			if (e.done || e.applyDone) && activeResult.FileTotal > 0 {
				line += fmt.Sprintf("  ~%d/%d", activeResult.FileChanged, activeResult.FileTotal)
			} else if !e.done && e.fileTotal > 0 {
				bar := liveProgressBar(e.fileScanned, e.fileTotal, 16)
				if e.currentFile != "" {
					bar += "  " + filepath.Base(e.currentFile)
				}
				line += "  " + bar
			}
			if (e.done || e.applyDone) && len(activeResult.Warnings) > 0 {
				line += "  ⚠"
			}
			treeLines = append(treeLines, treeLine{entryIdx: i, text: line})
			appendOutputLines(e)
		} else {
			allSettled := true
			anyApplying := false
			changed, deployed, failed := 0, 0, 0
			for _, i := range g.indices {
				e := m.liveEntries[i]
				if e.applying {
					anyApplying = true
					allSettled = false
				} else if e.applyDone {
					switch e.applyResult.Status {
					case cue.StatusChanged:
						deployed++
					case cue.StatusFailed:
						failed++
					}
				} else if !e.done {
					allSettled = false
				} else if e.result.Status == cue.StatusChanged {
					changed++
				}
			}
			var info string
			switch {
			case anyApplying || !allSettled:
				info = colorize(spinFrames[m.spinFrame], ansiYellow)
			case failed > 0:
				info = colorize(fmt.Sprintf("%d failed", failed), ansiRed)
			case deployed > 0:
				info = colorize(fmt.Sprintf("%d deployed", deployed), ansiGreen)
			case changed > 0:
				info = colorize(fmt.Sprintf("%d changed", changed), ansiGreen)
			default:
				info = colorize("all in sync", ansiDim)
			}
			treeLines = append(treeLines, treeLine{entryIdx: -1, text: fmt.Sprintf("● %s   %s", g.label, info)})
			for _, i := range g.indices {
				e := m.liveEntries[i]
				sym := liveSymbol(e, m.spinFrame)
				name := e.info.Name
				if e.done {
					name = e.result.CueName
				}
				line := fmt.Sprintf("    %s  %s", sym, name)
				activeResult := e.result
				if e.applyDone {
					activeResult = e.applyResult
				}
				if (e.done || e.applyDone) && activeResult.FileTotal > 0 {
					line += fmt.Sprintf("  ~%d/%d", activeResult.FileChanged, activeResult.FileTotal)
				} else if !e.done && e.fileTotal > 0 {
					bar := liveProgressBar(e.fileScanned, e.fileTotal, 16)
					if e.currentFile != "" {
						bar += "  " + filepath.Base(e.currentFile)
					}
					line += "  " + bar
				}
				if (e.done || e.applyDone) && len(activeResult.Warnings) > 0 {
					line += "  ⚠"
				}
				treeLines = append(treeLines, treeLine{entryIdx: i, text: line})
				appendOutputLines(e)
			}
		}
	}

	// Compute display window.
	// reserved: title(1) + blank(1) + rule(1) + footer(1) = 4
	reserved := 4
	maxLines := m.height - reserved
	if maxLines < 1 {
		maxLines = 1
	}

	// Auto-scroll: find the line index of the applying entry and centre on it.
	if m.liveAutoScroll && m.liveCursor < 0 {
		// Find first treeLines entry belonging to the applying liveEntry.
		applyingIdx := -1
		for _, e := range m.liveEntries {
			if e.applying {
				for ti, tl := range treeLines {
					if tl.entryIdx == applyingIdx {
						_ = ti
					}
				}
				break
			}
		}
		// Find the liveEntry index of the applying step.
		for i, e := range m.liveEntries {
			if e.applying {
				applyingIdx = i
				break
			}
		}
		if applyingIdx >= 0 {
			// Find the treeLine for this entry.
			for ti, tl := range treeLines {
				if tl.entryIdx == applyingIdx {
					scroll := ti - maxLines/2
					if scroll < 0 {
						scroll = 0
					}
					m.liveScroll = scroll
					break
				}
			}
		}
	}

	scroll := m.liveScroll
	if scroll > len(treeLines)-maxLines {
		scroll = len(treeLines) - maxLines
	}
	if scroll < 0 {
		scroll = 0
	}
	end := scroll + maxLines
	if end > len(treeLines) {
		end = len(treeLines)
	}
	for _, tl := range treeLines[scroll:end] {
		sb.WriteString(tl.text + "\n")
	}

	ruleWidth := m.width - 1
	if ruleWidth < 10 {
		ruleWidth = 49
	}
	sb.WriteString(strings.Repeat("─", ruleWidth) + "\n")
	if len(m.liveEntries) == 0 {
		fmt.Fprintf(&sb, "%s %s…\n", phaseGerund(m.phaseLabel), m.target)
	} else {
		applyDone, applyTotal := 0, 0
		for _, e := range m.liveEntries {
			if e.applyDone || e.applying || e.result.Status != cue.StatusEqual {
				applyTotal++
				if e.applyDone {
					applyDone++
				}
			}
		}
		if applyTotal > 0 {
			fmt.Fprintf(&sb, "deploying %d / %d   q quit\n", applyDone, applyTotal)
		} else {
			fmt.Fprintf(&sb, "%d / %d   q quit\n", checked, len(m.liveEntries))
		}
	}
	return sb.String()
}

// ── browse view ───────────────────────────────────────────────────────────────

func (m phaseModel) View() string {
	if m.quitting {
		return ""
	}
	if m.checking {
		return m.viewLive()
	}
	return m.viewBrowse()
}

func (m phaseModel) viewBrowse() string {
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

	changed, equal, failed := countResults(m.allResults())
	if failed > 0 {
		fmt.Fprintf(&sb, "%d changed · %d unchanged · %d failed   %.1fs\n",
			changed, equal, failed, m.total.Seconds())
	} else {
		fmt.Fprintf(&sb, "%d changed · %d unchanged   %.1fs\n",
			changed, equal, m.total.Seconds())
	}

	if m.searchMode {
		fmt.Fprintf(&sb, "\n/ %s█\n", m.searchQuery)
	} else if m.errMsg != "" {
		fmt.Fprintf(&sb, "\n%sFAILED: %s%s  q quit\n", ansiRed, m.errMsg, ansiReset)
	} else if m.confirming {
		fmt.Fprintf(&sb, "\n%s? [y/N]\n", m.nextPhaseLabel)
	} else if m.contentFocus {
		fmt.Fprintf(&sb, "\n↑↓ scroll   →← tab   ← back   esc back   q quit\n")
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
		var compensateHint string
		if m.confirmCh != nil && m.runBlockMsg == "" {
			if m.compensate {
				compensateHint = "  o on_error:compensate"
			} else {
				compensateHint = "  o on_error:halt"
			}
		}
		var runHint string
		if m.runBlockMsg != "" {
			firstLine := m.runBlockMsg
			if idx := strings.IndexByte(m.runBlockMsg, '\n'); idx >= 0 {
				firstLine = m.runBlockMsg[:idx]
			}
			runHint = fmt.Sprintf("  %s⚠ %s — use --allow-dirty%s", ansiYellow, firstLine, ansiReset)
		} else if m.confirmCh != nil && m.nextPhaseLabel != "" {
			runHint = fmt.Sprintf("  %c %s", rune(m.nextPhaseLabel[0]), m.nextPhaseLabel)
		}
		var contentHint string
		if m.cursorHasOverflow() {
			contentHint = "  enter content-scroll"
		}
		fmt.Fprintf(&sb, "\n↑↓ navigate  →← tab/collapse  +/- all  / search%s%s%s%s  q quit\n", skippedHint, compensateHint, contentHint, runHint)
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
func tabBar(sc phaseScenario, ci int, isCursor bool) string {
	hasDetails := len(sc.detailLines[ci]) > 0
	hasExec := len(sc.execLines[ci]) > 0
	if !hasDetails && !hasExec {
		return ""
	}
	active := sc.activeTab[ci]

	renderTab := func(name string, t tabID) string {
		if t == active {
			label := "[" + name + "]"
			if isCursor {
				// Stay within the outer reverse context; reset intensity only (\033[22m)
				// so the reverse background is preserved for the inactive tab too.
				return ansiBold + label + "\033[22m"
			}
			return ansiBold + label + ansiReset
		}
		// Inactive but available: dim
		if (t == tabDetails && hasDetails) || (t == tabExec && hasExec) {
			if isCursor {
				return ansiDim + name + "\033[22m"
			}
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
func activeTabLines(sc phaseScenario, ci int) []string {
	switch sc.activeTab[ci] {
	case tabDetails:
		return sc.detailLines[ci]
	case tabExec:
		return sc.execLines[ci]
	}
	return nil
}

func newPhaseModel(
	results []cue.Result,
	target string,
	total time.Duration,
	verbose bool,
	minfo *output.ManifestInfo,
	phaseLabel string,
	nextPhaseLabel string,
) phaseModel {
	type groupEntry struct {
		name  string
		label string
		rows  []cue.Result
	}
	var groups []groupEntry
	groupIdx := map[string]int{}
	for _, r := range results {
		key := r.ScenarioName
		label := r.ScenarioDesc
		if label == "" {
			label = key
		}
		if i, ok := groupIdx[key]; ok {
			groups[i].rows = append(groups[i].rows, r)
		} else {
			groupIdx[key] = len(groups)
			groups = append(groups, groupEntry{key, label, []cue.Result{r}})
		}
	}

	scenarios := make([]phaseScenario, len(groups))
	for i, g := range groups {
		n := len(g.rows)
		detailL := make([][]string, n)
		execL := make([][]string, n)
		for j, r := range g.rows {
			detailL[j] = output.CueInfoLines(r, true, minfo)
			execL[j] = output.CueExecLines(r, verbose)
		}
		scenarios[i] = phaseScenario{
			name:             g.name,
			label:            g.label,
			results:          g.rows,
			expanded:         false,
			cueExpanded:      make([]bool, n),
			activeTab:        make([]tabID, n),
			detailLines:      detailL,
			execLines:        execL,
			cueContentOffset: make([]int, n),
		}
	}

	m := phaseModel{
		scenarios:      scenarios,
		target:         target,
		total:          total,
		verbose:        verbose,
		minfo:          minfo,
		phaseLabel:     phaseLabel,
		nextPhaseLabel: nextPhaseLabel,
		width:          80,
		height:         24,
		startedAt:      time.Now(),
	}
	m.visible = m.buildVisible()
	return m
}

func (m phaseModel) buildVisible() []phaseVisItem {
	var items []phaseVisItem
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
			items = append(items, phaseVisItem{kind: phaseKindMergedScenario, scenarioIdx: si, cueIdx: ci})
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

		items = append(items, phaseVisItem{kind: phaseKindScenario, scenarioIdx: si, cueIdx: -1})
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
			items = append(items, phaseVisItem{kind: phaseKindCue, scenarioIdx: si, cueIdx: ci})
		}
	}
	return items
}

// renderListAndCursorLine renders all visible items to a flat line slice.
// cursorLine is the index of the first line belonging to the cursor item.
func (m phaseModel) renderListAndCursorLine() (lines []string, cursorLine int) {
	cursorLine = 0
	// Determine maxVisible for content scroll: half the list height.
	reserved := 6
	if m.searchMode {
		reserved = 7
	}
	listHeight := m.height - reserved
	if listHeight < 1 {
		listHeight = 1
	}
	maxVisible := listHeight / 2
	if maxVisible < 3 {
		maxVisible = 3
	}

	for vi, item := range m.visible {
		isCursor := vi == m.cursor
		if isCursor {
			cursorLine = len(lines)
		}
		contentOffset := 0
		if isCursor && m.contentFocus && item.kind != phaseKindScenario {
			si, ci := item.scenarioIdx, item.cueIdx
			if ci >= 0 && ci < len(m.scenarios[si].cueContentOffset) {
				contentOffset = m.scenarios[si].cueContentOffset[ci]
			}
		}
		itemLines := m.renderItem(item, isCursor, contentOffset, maxVisible)
		lines = append(lines, itemLines...)
	}
	return
}

// renderItem returns the display lines for a single visible item.
// contentOffset is the scroll offset into active tab content (only for cursor item in content mode).
// maxVisible is the max content lines to show at once.
// An expanded cue row includes its active tab content lines beneath it.
func (m phaseModel) renderItem(item phaseVisItem, isCursor bool, contentOffset, maxVisible int) []string {
	si, ci := item.scenarioIdx, item.cueIdx

	var header string
	switch item.kind {
	case phaseKindScenario:
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

	case phaseKindMergedScenario:
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

	case phaseKindCue:
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
	if item.kind != phaseKindScenario {
		sc := m.scenarios[si]
		if sc.cueExpanded[ci] {
			all := activeTabLines(sc, ci)
			total := len(all)
			if isCursor && m.contentFocus && total > maxVisible {
				// Clamp offset.
				if contentOffset < 0 {
					contentOffset = 0
				}
				if contentOffset > total-maxVisible {
					contentOffset = total - maxVisible
				}
				if contentOffset > 0 {
					lines = append(lines, colorize(fmt.Sprintf("      ↑ %d above", contentOffset), ansiDim))
				}
				visible := all[contentOffset:]
				if len(visible) > maxVisible {
					visible = visible[:maxVisible]
				}
				for _, l := range visible {
					lines = append(lines, "      "+l)
				}
				remaining := total - contentOffset - len(visible)
				if remaining > 0 {
					lines = append(lines, colorize(fmt.Sprintf("      ↓ %d more   ↑↓ scroll   esc back", remaining), ansiDim))
				}
			} else {
				for _, l := range all {
					lines = append(lines, "      "+l)
				}
				// Show scroll hint when content overflows and not yet in content mode.
				if isCursor && !m.contentFocus && total > maxVisible {
					lines = append(lines, colorize("      ↓ more   enter content-scroll", ansiDim))
				}
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

func (m phaseModel) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.searchMode = false
		m.searchQuery = ""
		m.visible = m.buildVisible()
		m.cursor = clamp(m.cursor, 0, len(m.visible)-1)
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
		m.cursor = clamp(m.cursor, 0, len(m.visible)-1)
	case tea.KeyRunes:
		m.searchQuery += msg.String()
		m.visible = m.buildVisible()
		m.cursor = 0
	}
	return m, nil
}

// updateConfirm handles keypresses while the "run? [y/N]" prompt is shown.
func (m phaseModel) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	override := "halt"
	if m.compensate {
		override = "compensate"
	}
	switch {
	case msg.String() == "y" || msg.String() == "Y":
		select {
		case m.confirmCh <- confirmDecision{proceed: true, overrideOnError: override}:
		default:
		}
		m.confirmCh = nil
		m.confirming = false
		m.phaseLabel = m.nextPhaseLabel
		m.nextPhaseLabel = ""
		m.checking = true
		m.liveEntries = nil
		m.startedAt = time.Now()
		return m, liveTick()
	case msg.Type == tea.KeyCtrlC:
		select {
		case m.confirmCh <- confirmDecision{proceed: false}:
		default:
		}
		m.quitting = true
		return m, tea.Quit
	default: // Enter, n, N, Escape, any other key → cancel
		m.confirming = false
		return m, nil
	}
}

func (m phaseModel) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyUp, tea.KeyCtrlU:
		if m.contentFocus {
			m = m.scrollContent(-1)
		} else {
			if m.cursor > 0 {
				m.cursor--
			}
		}
	case tea.KeyDown, tea.KeyCtrlD:
		if m.contentFocus {
			m = m.scrollContent(1)
		} else {
			if m.cursor < len(m.visible)-1 {
				m.cursor++
			}
		}
	case tea.KeyCtrlP:
		if m.cursor > 0 {
			m.cursor--
		}
	case tea.KeyCtrlN:
		if m.cursor < len(m.visible)-1 {
			m.cursor++
		}
	case tea.KeyEscape:
		if m.contentFocus {
			m.contentFocus = false
			return m, nil
		}
	case tea.KeyEnter:
		// Toggle content focus if expanded item has overflowing content.
		if m.cursorHasOverflow() {
			m.contentFocus = !m.contentFocus
			return m, nil
		}
		m = m.actionRight()
		return m, nil
	case tea.KeyTab:
		m = m.actionRight()
	case tea.KeyRight:
		// In content mode, → switches to next tab and resets offset.
		if m.contentFocus {
			m = m.actionRightTabOnly()
		} else {
			m = m.actionRight()
		}
	case tea.KeyLeft, tea.KeyBackspace:
		if m.contentFocus {
			m = m.actionLeftContentMode()
		} else {
			m = m.actionLeft()
		}
	case tea.KeyShiftTab:
		m = m.jumpPrevScenario()
	case tea.KeyCtrlC:
		if m.confirmCh != nil {
			select {
			case m.confirmCh <- confirmDecision{proceed: false}:
			default:
			}
		}
		m.quitting = true
		return m, tea.Quit
	case tea.KeyRunes:
		switch msg.String() {
		case "k":
			if m.contentFocus {
				m = m.scrollContent(-1)
			} else if m.cursor > 0 {
				m.cursor--
			}
		case "j":
			if m.contentFocus {
				m = m.scrollContent(1)
			} else if m.cursor < len(m.visible)-1 {
				m.cursor++
			}
		case "c", "-":
			m = m.compactAll()
		case "d", "+":
			m = m.expandAll()
		case "h":
			m.hideSkipped = !m.hideSkipped
			m.visible = m.buildVisible()
			m.cursor = clamp(m.cursor, 0, len(m.visible)-1)
		case "/":
			m.searchMode = true
			m.searchQuery = ""
		case "o":
			if m.confirmCh != nil {
				m.compensate = !m.compensate
				m.compensateToggled = true
			}
		case "q":
			if m.confirmCh != nil {
				select {
				case m.confirmCh <- confirmDecision{proceed: false}:
				default:
				}
			}
			m.quitting = true
			return m, tea.Quit
		default:
			if m.confirmCh != nil && m.nextPhaseLabel != "" &&
				msg.String() == string(rune(m.nextPhaseLabel[0])) {
				if m.runBlockMsg != "" {
					// Run is blocked — show full reason as error.
					m.errMsg = m.runBlockMsg + "\nuse --allow-dirty to deploy anyway"
				} else {
					m.confirming = true
				}
			}
		}
	}
	return m, nil
}

// actionRight handles → / enter / tab:
// - Scenario: toggle expand
// - Cue collapsed: expand, set first tab
// - Cue expanded: next tab (cycles)
func (m phaseModel) actionRight() phaseModel {
	if len(m.visible) == 0 {
		return m
	}
	item := m.visible[m.cursor]
	si, ci := item.scenarioIdx, item.cueIdx

	switch item.kind {
	case phaseKindScenario:
		m.scenarios[si].expanded = !m.scenarios[si].expanded

	case phaseKindMergedScenario, phaseKindCue:
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
	m.cursor = clamp(m.cursor, 0, len(m.visible)-1)
	return m
}

// actionLeft handles ←:
// - Scenario: collapse
// - Cue expanded at tab > 0: go to previous tab
// - Cue expanded at tab 0: collapse
// - Cue collapsed: collapse parent scenario
func (m phaseModel) actionLeft() phaseModel {
	if len(m.visible) == 0 {
		return m
	}
	item := m.visible[m.cursor]
	si, ci := item.scenarioIdx, item.cueIdx

	switch item.kind {
	case phaseKindScenario:
		m.scenarios[si].expanded = false

	case phaseKindMergedScenario, phaseKindCue:
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
		} else if item.kind == phaseKindCue {
			// Collapsed cue: collapse the parent scenario.
			m.scenarios[si].expanded = false
		}
	}
	m.visible = m.buildVisible()
	m.cursor = clamp(m.cursor, 0, len(m.visible)-1)
	return m
}

// cursorMaxVisible returns the maxVisible lines for content scroll.
func (m phaseModel) cursorMaxVisible() int {
	reserved := 6
	if m.searchMode {
		reserved = 7
	}
	listHeight := m.height - reserved
	if listHeight < 1 {
		listHeight = 1
	}
	mv := listHeight / 2
	if mv < 3 {
		mv = 3
	}
	return mv
}

// cursorHasOverflow reports whether the cursor item is expanded and has more
// content lines than maxVisible.
func (m phaseModel) cursorHasOverflow() bool {
	if len(m.visible) == 0 {
		return false
	}
	item := m.visible[m.cursor]
	if item.kind == phaseKindScenario || item.cueIdx < 0 {
		return false
	}
	si, ci := item.scenarioIdx, item.cueIdx
	sc := m.scenarios[si]
	if !sc.cueExpanded[ci] {
		return false
	}
	return len(activeTabLines(sc, ci)) > m.cursorMaxVisible()
}

// scrollContent adjusts the content offset for the cursor item by delta.
func (m phaseModel) scrollContent(delta int) phaseModel {
	if len(m.visible) == 0 {
		return m
	}
	item := m.visible[m.cursor]
	if item.kind == phaseKindScenario || item.cueIdx < 0 {
		return m
	}
	si, ci := item.scenarioIdx, item.cueIdx
	if ci >= len(m.scenarios[si].cueContentOffset) {
		return m
	}
	total := len(activeTabLines(m.scenarios[si], ci))
	maxVis := m.cursorMaxVisible()
	off := m.scenarios[si].cueContentOffset[ci] + delta
	if off < 0 {
		off = 0
	}
	if off > total-maxVis {
		off = total - maxVis
	}
	if off < 0 {
		off = 0
	}
	m.scenarios[si].cueContentOffset[ci] = off
	return m
}

// actionRightTabOnly cycles to the next tab and resets content offset (used in content mode).
func (m phaseModel) actionRightTabOnly() phaseModel {
	if len(m.visible) == 0 {
		return m
	}
	item := m.visible[m.cursor]
	si, ci := item.scenarioIdx, item.cueIdx
	if item.kind == phaseKindScenario || ci < 0 {
		return m
	}
	sc := m.scenarios[si]
	hasDetails := len(sc.detailLines[ci]) > 0
	hasExec := len(sc.execLines[ci]) > 0
	cur := sc.activeTab[ci]
	for delta := tabID(1); delta < tabCount; delta++ {
		next := (cur + delta) % tabCount
		if next == tabDetails && hasDetails {
			m.scenarios[si].activeTab[ci] = next
			m.scenarios[si].cueContentOffset[ci] = 0
			return m
		}
		if next == tabExec && hasExec {
			m.scenarios[si].activeTab[ci] = next
			m.scenarios[si].cueContentOffset[ci] = 0
			return m
		}
	}
	return m
}

// actionLeftContentMode handles ← in content mode:
// if on tab > 0: go to previous tab and reset offset; if on tab 0: exit content mode.
func (m phaseModel) actionLeftContentMode() phaseModel {
	if len(m.visible) == 0 {
		return m
	}
	item := m.visible[m.cursor]
	si, ci := item.scenarioIdx, item.cueIdx
	if item.kind == phaseKindScenario || ci < 0 {
		m.contentFocus = false
		return m
	}
	sc := m.scenarios[si]
	cur := sc.activeTab[ci]
	for t := int(cur) - 1; t >= 0; t-- {
		if tabID(t) == tabDetails && len(sc.detailLines[ci]) > 0 {
			m.scenarios[si].activeTab[ci] = tabID(t)
			m.scenarios[si].cueContentOffset[ci] = 0
			return m
		}
		if tabID(t) == tabExec && len(sc.execLines[ci]) > 0 {
			m.scenarios[si].activeTab[ci] = tabID(t)
			m.scenarios[si].cueContentOffset[ci] = 0
			return m
		}
	}
	// Already on first/only tab: exit content mode.
	m.contentFocus = false
	return m
}

func (m phaseModel) jumpPrevScenario() phaseModel {
	for i := m.cursor - 1; i >= 0; i-- {
		k := m.visible[i].kind
		if k == phaseKindScenario || k == phaseKindMergedScenario {
			m.cursor = i
			return m
		}
	}
	return m
}

func (m phaseModel) compactAll() phaseModel {
	for i := range m.scenarios {
		m.scenarios[i].expanded = false
		for j := range m.scenarios[i].cueExpanded {
			m.scenarios[i].cueExpanded[j] = false
		}
	}
	m.visible = m.buildVisible()
	m.cursor = clamp(m.cursor, 0, len(m.visible)-1)
	return m
}

func (m phaseModel) expandAll() phaseModel {
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
	m.cursor = clamp(m.cursor, 0, len(m.visible)-1)
	return m
}

func (m phaseModel) allResults() []cue.Result {
	var all []cue.Result
	for _, sc := range m.scenarios {
		all = append(all, sc.results...)
	}
	return all
}

func (m phaseModel) countSkipped() int {
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

func clamp(v, lo, hi int) int {
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

// RunLiveTUI launches a live TUI that runs phase1 immediately, then transitions to
// browse mode. If phase2 is non-nil the browse footer shows the advance key (first
// letter of phase2.Label); pressing it triggers a confirmation then runs phase2.
//
// Typical usage:
//   - rdiff:                    phase1={check,…}, phase2=nil
//   - run (normal):             phase1={check,…}, phase2=&{run,…}
//   - run --run-without-check:  phase1={run,…},   phase2=nil
func RunLiveTUI(
	ctx context.Context,
	target string,
	verbose bool,
	level output.Level,
	minfo *output.ManifestInfo,
	gitSHA string,
	phase1 PhaseFunc,
	phase2 *PhaseFunc,
	compensate bool,
	runBlockMsg string,
) error {
	var confirmCh chan confirmDecision
	if phase2 != nil {
		confirmCh = make(chan confirmDecision, 1)
	}

	var nextLabel string
	if phase2 != nil {
		nextLabel = phase2.Label
	}
	m := phaseModel{
		target:             target,
		verbose:            verbose,
		minfo:              minfo,
		gitSHA:             gitSHA,
		phaseLabel:         phase1.Label,
		nextPhaseLabel:     nextLabel,
		width:              80,
		height:             24,
		confirmCh:          confirmCh,
		compensate:         compensate,
		compensateInferred: compensate,
		runBlockMsg:        runBlockMsg,
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
	var curScenario, curCue atomic.Value
	ctx = cue.WithApplyStep(ctx, func(scenario, cueName string) {
		curScenario.Store(scenario)
		curCue.Store(cueName)
		prog.Send(liveApplyStepMsg{scenarioName: scenario, cueName: cueName})
	})
	ctx = cue.WithApplyResult(ctx, func(r cue.Result) {
		prog.Send(liveApplyResultMsg{result: r})
	})
	ctx = cue.WithOutputLine(ctx, func(line string, isStderr bool) {
		s, _ := curScenario.Load().(string)
		c, _ := curCue.Load().(string)
		prog.Send(liveOutputLineMsg{scenarioName: s, cueName: c, line: line, isStderr: isStderr})
	})

	type runResult struct {
		results []cue.Result
		elapsed time.Duration
		err     error
	}
	ch := make(chan runResult, 1)
	go func() {
		// Phase 1.
		results, elapsed, err := phase1.Fn(ctx)

		var msgConfirmCh chan confirmDecision
		var msgNextLabel string
		if phase2 != nil && err == nil {
			msgConfirmCh = confirmCh
			msgNextLabel = phase2.Label
		}
		prog.Send(liveRunCompleteMsg{
			results: results, elapsed: elapsed, err: err,
			confirmCh: msgConfirmCh, phaseLabel: phase1.Label, nextPhaseLabel: msgNextLabel,
		})

		if phase2 == nil {
			ch <- runResult{results, elapsed, err}
			return
		}

		// Wait for user to confirm or quit.
		decision := <-confirmCh
		if !decision.proceed {
			ch <- runResult{results, elapsed, nil}
			return
		}

		// Apply on_error override from the toggle before running phase 2.
		if phase2.OnOverrideSet != nil {
			phase2.OnOverrideSet(decision.overrideOnError)
		}

		// Phase 2. Reuses ctx with live callbacks so the runner's internal pre-check
		// fires livePrePhaseMsg/liveCheckResultMsg again for a fresh live view.
		r2, e2, err2 := phase2.Fn(ctx)
		prog.Send(liveRunCompleteMsg{
			results: r2, elapsed: e2, err: err2,
			confirmCh: nil, phaseLabel: phase2.Label, nextPhaseLabel: "",
		})
		ch <- runResult{r2, e2, err2}
	}()

	_, tuiErr := prog.Run()

	// If the TUI exited before the user confirmed, unblock the goroutine.
	if confirmCh != nil {
		select {
		case confirmCh <- confirmDecision{proceed: false}:
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

