// internal/runner/manifest_test.go
package runner_test

import (
	"strings"
	"testing"

	"git.disroot.org/jmy/regis/internal/runner"
)

func TestNewReleaseID_format(t *testing.T) {
	id := runner.NewStateID()
	if !strings.HasPrefix(id, "v") {
		t.Errorf("release ID must start with 'v', got %q", id)
	}
	// "v20060102-150405" is 16 chars minimum
	if len(id) < 16 {
		t.Errorf("release ID too short: want >= 16 chars, got %d (%q)", len(id), id)
	}
}
