// internal/tui/scenariomenu.go
package tui

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/output"
	"git.disroot.org/jmy/regis/internal/score"
)

// RunScenarioMenu presents a scenario picker and returns the chosen name.
// Returns ("", nil) if the user quit without selecting.
func RunScenarioMenu(cfg *config.Config, level output.Level) (string, error) {
	names := score.SortedScenarioNames(cfg, "yaml")
	if len(names) == 0 {
		return "", fmt.Errorf("no scenarios found in config")
	}

	items := make([]menuItem, len(names))
	for i, n := range names {
		sc := cfg.Scenarios[n]
		items[i] = menuItem{
			name:     n,
			desc:     sc.Describe,
			cueCount: len(sc.Cues),
			requires: []string(sc.Requires),
		}
	}

	if level >= output.Level2 {
		return runMenuTUI(items)
	}
	return runMenuPlain(items)
}

// menuItem is one selectable row.
type menuItem struct {
	name     string
	desc     string
	cueCount int
	requires []string
}

// ── plain (Level1) ────────────────────────────────────────────────────────────

func runMenuPlain(items []menuItem) (string, error) {
	fmt.Println("  regis — choose a scenario")
	fmt.Println()

	nameWidth := 0
	for _, it := range items {
		if len(it.name) > nameWidth {
			nameWidth = len(it.name)
		}
	}

	for i, it := range items {
		num := fmt.Sprintf("%d", i+1)
		if it.desc != "" {
			fmt.Printf("    %-3s  %-*s  %s\n", num, nameWidth, it.name, it.desc)
		} else {
			fmt.Printf("    %-3s  %s\n", num, it.name)
		}
	}

	fmt.Printf("\n  > ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return "", nil
	}
	tok := strings.TrimSpace(scanner.Text())
	if tok == "" || tok == "q" {
		return "", nil
	}
	n, err := strconv.Atoi(tok)
	if err != nil || n < 1 || n > len(items) {
		return "", fmt.Errorf("invalid selection %q", tok)
	}
	return items[n-1].name, nil
}

// ── TUI (Level2) ──────────────────────────────────────────────────────────────

type menuModel struct {
	items  []menuItem
	cursor int
	chosen string
	width  int
}

func runMenuTUI(items []menuItem) (string, error) {
	m := &menuModel{items: items}
	p := tea.NewProgram(m)
	final, err := p.Run()
	if err != nil {
		return "", err
	}
	return final.(*menuModel).chosen, nil
}

func (m *menuModel) Init() tea.Cmd { return nil }

// columns returns the number of card columns that fit the current terminal width.
// Each card slot occupies (minCardWidth + gap) chars; the left margin accounts for the leading 2.
// Layout: 2(margin) + n*cw + (n-1)*2(gap)  =  n*(cw+2) = width
func (m *menuModel) columns() int {
	const minCard = 32
	w := m.width
	if w == 0 {
		return 1
	}
	for n := 3; n >= 2; n-- {
		if w >= n*(minCard+2) {
			return n
		}
	}
	return 1
}

// cardWidth returns the card width for n columns.
// Derived from: n*(cw+2) = width  →  cw = width/n - 2
func (m *menuModel) cardWidth(n int) int {
	if m.width == 0 {
		return 66
	}
	cw := m.width/n - 2
	if cw > 70 {
		cw = 70
	}
	if cw < 24 {
		cw = 24
	}
	return cw
}

func (m *menuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case tea.KeyMsg:
		cols := m.columns()
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			return m, tea.Quit
		case "up", "k":
			if m.cursor-cols >= 0 {
				m.cursor -= cols
			}
		case "down", "j":
			if m.cursor+cols < len(m.items) {
				m.cursor += cols
			}
		case "left", "h":
			if m.cursor > 0 {
				m.cursor--
			}
		case "right", "l":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case "enter", " ":
			m.chosen = m.items[m.cursor].name
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *menuModel) View() string {
	cols := m.columns()
	cw := m.cardWidth(cols)

	var sb strings.Builder
	sb.WriteString("\n  " + ansiBold + "regis" + ansiReset + " — choose a scenario\n\n")

	for rowStart := 0; rowStart < len(m.items); rowStart += cols {
		rowEnd := rowStart + cols
		if rowEnd > len(m.items) {
			rowEnd = len(m.items)
		}

		// Render each card in this row into lines.
		rowCards := make([][]string, rowEnd-rowStart)
		for i := range rowCards {
			rowCards[i] = cardLines(m.items[rowStart+i], rowStart+i == m.cursor, cw)
		}

		// Join card lines horizontally: margin + card + gap + card + ...
		for li := 0; li < len(rowCards[0]); li++ {
			sb.WriteString("  ")
			for ci, lines := range rowCards {
				sb.WriteString(lines[li])
				if ci < len(rowCards)-1 {
					sb.WriteString("  ") // gap between columns
				}
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	hint := "↑/↓ row · ←/→ col · enter run · q quit"
	if cols == 1 {
		hint = "↑/↓ navigate · enter run · q quit"
	}
	sb.WriteString("  " + colorize(hint, ansiDim) + "\n")
	return sb.String()
}

// cardLines renders a single card as a slice of fixed-width strings (no trailing newline,
// no leading margin). All lines are exactly cardWidth visible characters wide.
//
// Selected cards use a double-line border (╔═╗) in bold green.
// Unselected cards use a single-line border (╭─╮) in dim.
func cardLines(it menuItem, selected bool, cardWidth int) []string {
	var tl, tr, bl, br, h, v string
	var borderColor string
	if selected {
		tl, tr, bl, br, h, v = "╔", "╗", "╚", "╝", "═", "║"
		borderColor = ansiGreen + ansiBold
	} else {
		tl, tr, bl, br, h, v = "╭", "╮", "╰", "╯", "─", "│"
		borderColor = ansiDim
	}
	cb := func(s string) string { return colorize(s, borderColor) }

	innerWidth := cardWidth - 4 // border + space + content + space + border

	// Top / bottom borders span the full card width.
	top := cb(tl + strings.Repeat(h, cardWidth-2) + tr)
	bottom := cb(bl + strings.Repeat(h, cardWidth-2) + br)

	// Title line — bold green when selected, bold when not.
	titleColor := ansiBold
	if selected {
		titleColor = ansiBold + ansiGreen
	}
	titleStr := colorize(padRight(truncate(it.name, innerWidth), innerWidth), titleColor)
	titleLine := fmt.Sprintf("%s %s %s", cb(v), titleStr, cb(v))

	// Description line — full brightness when selected, dim when not.
	plainDesc := padRight(truncate(it.desc, innerWidth), innerWidth)
	var descStr string
	if selected {
		descStr = plainDesc
	} else {
		descStr = colorize(plainDesc, ansiDim)
	}
	descLine := fmt.Sprintf("%s %s %s", cb(v), descStr, cb(v))

	// Meta line: step count · needs: ...
	meta := cardMeta(it)
	metaLine := fmt.Sprintf("%s %s %s", cb(v), colorize(padRight(meta, innerWidth), ansiDim), cb(v))

	return []string{top, titleLine, descLine, metaLine, bottom}
}

func cardMeta(it menuItem) string {
	var parts []string
	switch it.cueCount {
	case 0:
	case 1:
		parts = append(parts, "1 step")
	default:
		parts = append(parts, fmt.Sprintf("%d steps", it.cueCount))
	}
	if len(it.requires) > 0 {
		parts = append(parts, "needs: "+strings.Join(it.requires, ", "))
	}
	return strings.Join(parts, "  ·  ")
}

func padRight(s string, width int) string {
	n := utf8.RuneCountInString(s)
	if n >= width {
		return s
	}
	return s + strings.Repeat(" ", width-n)
}

func truncate(s string, width int) string {
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	if width > 3 {
		return string(runes[:width-3]) + "..."
	}
	return string(runes[:width])
}
