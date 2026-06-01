// internal/tui/wizard_test.go
package tui_test

import (
	"testing"
	"git.disroot.org/jmy/regis/internal/tui"
)

func TestWizardModel_initialState(t *testing.T) {
	m := tui.NewWizardModel()
	if m.Step() != tui.StepHost {
		t.Errorf("want first step = StepHost, got %v", m.Step())
	}
}

func TestWizardModel_setHost(t *testing.T) {
	m := tui.NewWizardModel()
	m2 := m.SetHost("prod.example.com")
	if m2.Host() != "prod.example.com" {
		t.Errorf("want host set, got %q", m2.Host())
	}
}
