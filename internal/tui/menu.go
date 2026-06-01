// internal/tui/menu.go
package tui

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

// RunWizard runs the interactive creation wizard.
// Returns the collected WizardModel when complete.
func RunWizard() (WizardModel, error) {
	m := NewWizardModel()
	p := tea.NewProgram(wizardTUI{model: m})
	finalModel, err := p.Run()
	if err != nil {
		return WizardModel{}, err
	}
	wt, ok := finalModel.(wizardTUI)
	if !ok {
		return WizardModel{}, fmt.Errorf("unexpected model type")
	}
	return wt.model, nil
}

// wizardTUI implements tea.Model.
type wizardTUI struct {
	model WizardModel
	input string
	err   string
}

func (w wizardTUI) Init() tea.Cmd { return nil }

func (w wizardTUI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return w, tea.Quit
		case "enter":
			w = w.advance()
			if w.model.Step() == StepDone {
				return w, tea.Quit
			}
		case "backspace":
			if len(w.input) > 0 {
				w.input = w.input[:len(w.input)-1]
			}
		default:
			if msg.Type == tea.KeyRunes {
				w.input += msg.String()
			}
		}
	}
	return w, nil
}

func (w wizardTUI) advance() wizardTUI {
	val := w.input
	w.input = ""
	w.err = ""
	switch w.model.Step() {
	case StepHost:
		if val == "" {
			w.err = "host is required"
			return w
		}
		w.model = w.model.SetHost(val)
	case StepUser:
		if val == "" {
			w.err = "user is required"
			return w
		}
		w.model = w.model.SetUser(val)
	case StepPort:
		if val == "" {
			val = "22"
		}
		w.model = w.model.SetPort(val)
	case StepDir:
		if val == "" {
			w.err = "remote dir is required"
			return w
		}
		w.model = w.model.SetDir(val)
	case StepScenarioName:
		if val == "" {
			val = "deploy"
		}
		w.model = w.model.SetScenario(val)
	}
	return w
}

func (w wizardTUI) View() string {
	prompts := map[WizardStep]string{
		StepHost:         "Target host (e.g. prod.example.com): ",
		StepUser:         "SSH user: ",
		StepPort:         "SSH port [22]: ",
		StepDir:          "Remote working dir (e.g. /opt/app): ",
		StepScenarioName: "First scenario name [deploy]: ",
		StepDone:         "Configuration complete.",
	}
	prompt := prompts[w.model.Step()]
	view := "regis init — let's set up your deployment\n\n" +
		prompt + w.input + "█"
	if w.err != "" {
		view += "\n\nError: " + w.err
	}
	return view
}

// RunConfigTUI opens the main menu TUI for editing an existing regis.yml.
// Minimal implementation for v1 — opens $EDITOR as fallback.
func RunConfigTUI(file string) error {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	fmt.Printf("Opening %s in %s (full TUI coming in v2)\n", file, editor)
	return nil
}
