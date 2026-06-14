// internal/tui/scenariomenu.go
package tui

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

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
		items[i] = menuItem{name: n, desc: cfg.Scenarios[n].Describe}
	}

	if level >= output.Level2 {
		return runMenuTUI(items)
	}
	return runMenuPlain(items)
}

// menuItem is one selectable row.
type menuItem struct {
	name string
	desc string
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

func (m *menuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
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
	nameWidth := 0
	for _, it := range m.items {
		if len(it.name) > nameWidth {
			nameWidth = len(it.name)
		}
	}

	var sb strings.Builder
	sb.WriteString("\n  " + ansiBold + "regis" + ansiReset + " — choose a scenario\n\n")

	for i, it := range m.items {
		if i == m.cursor {
			pointer := colorize("▶", ansiGreen)
			name := colorize(fmt.Sprintf("%-*s", nameWidth, it.name), ansiBold)
			if it.desc != "" {
				fmt.Fprintf(&sb, "  %s %s  %s\n", pointer, name, colorize(it.desc, ansiDim))
			} else {
				fmt.Fprintf(&sb, "  %s %s\n", pointer, name)
			}
		} else {
			name := fmt.Sprintf("%-*s", nameWidth, it.name)
			if it.desc != "" {
				fmt.Fprintf(&sb, "    %s  %s\n", name, colorize(it.desc, ansiDim))
			} else {
				fmt.Fprintf(&sb, "    %s\n", name)
			}
		}
	}

	sb.WriteString("\n  " + colorize("↑/↓ navigate · enter run · q quit", ansiDim) + "\n")
	return sb.String()
}
