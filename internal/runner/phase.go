// internal/runner/phase.go
package runner

import (
	"context"
	"time"

	"git.disroot.org/jmy/regis/internal/cue"
)

// PhaseFunc associates a display label with a phase function to be driven by any UI.
// Label (e.g. "check", "run") is shown in the status header.
// Fn runs the phase and returns its results.
// OnOverrideSet is called with the user-chosen on_error policy before Fn runs.
type PhaseFunc struct {
	Label         string
	Fn            func(context.Context) ([]cue.Result, time.Duration, error)
	OnOverrideSet func(overrideOnError string) // optional
}
