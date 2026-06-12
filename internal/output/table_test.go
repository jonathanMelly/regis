// internal/output/table_test.go
package output_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"git.disroot.org/jmy/regis/internal/cue"
	"git.disroot.org/jmy/regis/internal/output"
)

func TestRenderSummary_plain(t *testing.T) {
	results := []cue.Result{
		{CueName: "bin", ScenarioName: "saver", Nature: "binary", Status: cue.StatusChanged, Size: 1200000, Elapsed: 800 * time.Millisecond},
		{CueName: "env", ScenarioName: "saver", Nature: "secret", Status: cue.StatusEqual, Elapsed: 100 * time.Millisecond},
		{CueName: "gateway-conf", ScenarioName: "nginx", Nature: "config", Status: cue.StatusChanged, Elapsed: 200 * time.Millisecond},
	}
	out := output.RenderPlain(results, "prod-eu", 4200*time.Millisecond, false)
	if !strings.Contains(out, "prod-eu") {
		t.Error("expected target name in output")
	}
	if !strings.Contains(out, "bin") {
		t.Error("expected cue name 'bin'")
	}
	if !strings.Contains(out, "~") {
		t.Error("expected '~' status for changed (Level1/CI)")
	}
	if !strings.Contains(out, "=") {
		t.Error("expected '=' status for unchanged")
	}
}

func TestRenderTable_bordered(t *testing.T) {
	results := []cue.Result{
		{CueName: "bin", ScenarioName: "saver", ScenarioDesc: "Energy saver daemon", Nature: "binary", Status: cue.StatusChanged, Size: 1200000, Elapsed: 800 * time.Millisecond},
		{CueName: "env", ScenarioName: "saver", ScenarioDesc: "Energy saver daemon", Nature: "secret", Status: cue.StatusEqual, Elapsed: 100 * time.Millisecond},
		{CueName: "gateway-conf", ScenarioName: "nginx", ScenarioDesc: "Nginx gateway", Nature: "config", Status: cue.StatusChanged, Elapsed: 200 * time.Millisecond},
	}
	out := output.RenderTable(results, "prod-eu", 4200*time.Millisecond, false, output.Level1, false)

	// Must contain box-drawing characters
	if !strings.Contains(out, "┌") || !strings.Contains(out, "┐") {
		t.Error("expected top border ┌...┐")
	}
	if !strings.Contains(out, "└") || !strings.Contains(out, "┘") {
		t.Error("expected bottom border └...┘")
	}
	if !strings.Contains(out, "├") {
		t.Error("expected scenario separator ├")
	}
	// Scenario grouping
	if !strings.Contains(out, "Energy saver daemon") {
		t.Error("expected scenario label in table")
	}
	if !strings.Contains(out, "Nginx gateway") {
		t.Error("expected second scenario label in table")
	}
	// Cue rows
	if !strings.Contains(out, "bin") {
		t.Error("expected cue name 'bin'")
	}
	// Summary — rdiff mode (deployed=false) must say RDIFF
	if strings.Contains(out, "DEPLOYED") {
		t.Error("rdiff mode must not say DEPLOYED")
	}
	if !strings.Contains(out, "RDIFF") {
		t.Error("expected RDIFF summary in rdiff mode")
	}
	if !strings.Contains(out, "prod-eu") {
		t.Error("expected target name in summary")
	}
}

func TestRenderTable_deployed_says_DEPLOYED(t *testing.T) {
	results := []cue.Result{
		{CueName: "bin", ScenarioName: "s", Nature: "binary", Status: cue.StatusChanged},
	}
	out := output.RenderTable(results, "prod", 1*time.Second, true, output.Level1, false)
	if !strings.Contains(out, "DEPLOYED") {
		t.Error("deployed=true must say DEPLOYED")
	}
}

func TestRenderPlain_rdiff_says_RDIFF(t *testing.T) {
	results := []cue.Result{
		{CueName: "bin", ScenarioName: "s", Nature: "binary", Status: cue.StatusChanged},
	}
	out := output.RenderPlain(results, "prod", 1*time.Second, false)
	if strings.Contains(out, "DEPLOYED") {
		t.Error("rdiff mode must not say DEPLOYED")
	}
	if !strings.Contains(out, "RDIFF") {
		t.Error("expected RDIFF in rdiff mode")
	}
}

func TestAppendDetails_skipped_shows_stdout_reason(t *testing.T) {
	results := []cue.Result{
		{CueName: "env", Nature: "secret", Status: cue.StatusSkipped,
			Stdout: "local file .env.server not found — skipping (remote unchanged)"},
	}
	out := output.AppendDetails(results, false) // non-verbose
	if !strings.Contains(out, "env") {
		t.Error("expected cue name 'env' in output")
	}
	if !strings.Contains(out, "not found") {
		t.Error("expected skip reason in non-verbose output")
	}
}

func TestAppendDetails_binary_changed_shows_md5_mtime(t *testing.T) {
	ts := time.Date(2026, 5, 31, 10, 32, 0, 0, time.UTC)
	results := []cue.Result{
		{CueName: "bin", Nature: "binary", Status: cue.StatusChanged,
			LocalMD5:    "abc123def456789",
			RemoteMD5:   "deadbeefcafe12",
			LocalMtime:  ts,
			RemoteMtime: ts.Add(-24 * time.Hour)},
	}
	out := output.AppendDetails(results, false) // non-verbose
	if !strings.Contains(out, "local") {
		t.Error("expected 'local' in binary diff output")
	}
	if !strings.Contains(out, "remote") {
		t.Error("expected 'remote' in binary diff output")
	}
	if !strings.Contains(out, "abc123def4") {
		t.Error("expected local MD5 prefix in output")
	}
}

func TestRenderTree_scenarioGrouping(t *testing.T) {
	results := []cue.Result{
		{CueName: "mailway", ScenarioName: "mailway", ScenarioDesc: "Mailway", Status: cue.StatusChanged},
		{CueName: "mailway.yml", ScenarioName: "mailway", ScenarioDesc: "Mailway", Status: cue.StatusChanged},
		{CueName: "env", ScenarioName: "mailway", ScenarioDesc: "Mailway", Status: cue.StatusEqual},
		{CueName: "saver", ScenarioName: "saver", ScenarioDesc: "Saver", Status: cue.StatusChanged},
		{CueName: "env", ScenarioName: "saver", ScenarioDesc: "Saver", Status: cue.StatusEqual},
	}
	out := output.RenderTree(results, "prod", 4200*time.Millisecond, true, false, output.Level1)
	if !strings.Contains(out, "Mailway") {
		t.Error("expected scenario header 'Mailway'")
	}
	if !strings.Contains(out, "Saver") {
		t.Error("expected scenario header 'Saver'")
	}
	if !strings.Contains(out, "mailway.yml") {
		t.Error("expected cue name 'mailway.yml'")
	}
	// Mailway header must appear before Saver header.
	mi := strings.Index(out, "Mailway")
	si := strings.Index(out, "Saver")
	if mi >= si {
		t.Error("expected Mailway before Saver in output")
	}
}

func TestRenderTree_statusSymbols(t *testing.T) {
	results := []cue.Result{
		{CueName: "app", ScenarioName: "s", Status: cue.StatusChanged},
		{CueName: "cfg", ScenarioName: "s", Status: cue.StatusEqual},
		{CueName: "bad", ScenarioName: "s", Status: cue.StatusFailed},
		{CueName: "skip", ScenarioName: "s", Status: cue.StatusSkipped},
	}
	out := output.RenderTree(results, "prod", 1*time.Second, true, false, output.Level1)
	if !strings.Contains(out, "↑") {
		t.Error("expected ↑ for StatusChanged with no mtime")
	}
	if !strings.Contains(out, "=") {
		t.Error("expected = for StatusEqual")
	}
	if !strings.Contains(out, "✕") {
		t.Error("expected ✕ for StatusFailed")
	}
	if !strings.Contains(out, "/") {
		t.Error("expected / for StatusSkipped")
	}
}

func TestRenderTree_summaryLine(t *testing.T) {
	results := []cue.Result{
		{CueName: "a", ScenarioName: "s", Status: cue.StatusChanged},
		{CueName: "b", ScenarioName: "s", Status: cue.StatusChanged},
		{CueName: "c", ScenarioName: "s", Status: cue.StatusEqual},
		{CueName: "d", ScenarioName: "s", Status: cue.StatusEqual},
		{CueName: "e", ScenarioName: "s", Status: cue.StatusEqual},
	}
	out := output.RenderTree(results, "mytarget", 2100*time.Millisecond, true, false, output.Level1)
	if !strings.Contains(out, "2 changed") {
		t.Errorf("expected '2 changed' in summary; got:\n%s", out)
	}
	if !strings.Contains(out, "3 unchanged") {
		t.Errorf("expected '3 unchanged' in summary; got:\n%s", out)
	}
}

func TestRenderTree_detailsBinary_noVerbose(t *testing.T) {
	ts := time.Date(2026, 6, 2, 14, 30, 0, 0, time.UTC)
	results := []cue.Result{
		{
			CueName: "saver", ScenarioName: "saver", ScenarioDesc: "Saver",
			Nature: "binary", Status: cue.StatusChanged,
			LocalPath:   "bin/saver",
			RemotePath:  "/opt/app/saver",
			LocalMD5:    "a1b2c3d4e5f6",
			RemoteMD5:   "x9y8z7w6v5u4",
			LocalMtime:  ts,
			RemoteMtime: ts.Add(-24 * time.Hour),
		},
	}
	out := output.RenderTree(results, "prod", 1*time.Second, true, false, output.Level1)
	if !strings.Contains(out, "bin/saver") {
		t.Error("expected local path in binary detail (no -v)")
	}
	if !strings.Contains(out, "local") {
		t.Error("expected 'local' label in binary detail")
	}
	if !strings.Contains(out, "remote") {
		t.Error("expected 'remote' label in binary detail")
	}
}

func TestRenderTree_diffShownByDefault(t *testing.T) {
	results := []cue.Result{
		{
			CueName: "cfg", ScenarioName: "s", Nature: "config",
			Status: cue.StatusChanged,
			Diff:   "--- a\n+++ b\n@@ -1 +1 @@\n-old\n+new\n",
		},
	}
	// showDiff=true (default) → diff shown
	outDefault := output.RenderTree(results, "prod", 1*time.Second, true, false, output.Level1)
	if !strings.Contains(outDefault, "---") {
		t.Error("diff must appear when showDiff=true")
	}
	// showDiff=false (--no-diff) → diff suppressed
	outNoDiff := output.RenderTree(results, "prod", 1*time.Second, false, false, output.Level1)
	if strings.Contains(outNoDiff, "---") {
		t.Error("diff must NOT appear when showDiff=false")
	}
}

func TestDetectLevel_noTTY(t *testing.T) {
	// Test process has no TTY on stdout → Level1 regardless of env vars.
	level := output.DetectLevel()
	if level != output.Level1 {
		t.Errorf("expected Level1 with no TTY, got %d", level)
	}
}

func TestColorStatus_level1_noColor(t *testing.T) {
	// Level1 (no TTY) must return label unchanged regardless of status.
	for _, status := range []cue.Status{cue.StatusChanged, cue.StatusEqual, cue.StatusFailed, cue.StatusSkipped} {
		got := output.ColorStatus(status, "lbl", output.Level1)
		if got != "lbl" {
			t.Errorf("Level1 ColorStatus(%v) = %q, want plain %q", status, got, "lbl")
		}
	}
}

func TestColorStatus_level2_addsAnsi(t *testing.T) {
	// Level2 must wrap the label in ANSI codes (non-empty result that differs from plain label).
	got := output.ColorStatus(cue.StatusChanged, "lbl", output.Level2)
	if got == "lbl" {
		t.Error("Level2 ColorStatus(Changed) should add ANSI codes")
	}
	if !strings.Contains(got, "lbl") {
		t.Error("ColorStatus result must contain the original label")
	}
}

func TestCueDetailLines_errorShown(t *testing.T) {
	r := cue.Result{
		CueName: "cfg",
		Nature:  "config",
		Status:  cue.StatusFailed,
		Err:     fmt.Errorf("upload failed: connection reset"),
	}
	lines := output.CueDetailLines(r, false, false, nil)
	found := false
	for _, l := range lines {
		if strings.Contains(l, "upload failed") {
			found = true
		}
	}
	if !found {
		t.Errorf("error message must appear in CueDetailLines; got %v", lines)
	}
}

func TestCueDetailLines_warningsAlwaysShown(t *testing.T) {
	// Warnings must appear for ALL statuses, even equal and without verbose.
	for _, status := range []cue.Status{cue.StatusEqual, cue.StatusChanged, cue.StatusFailed} {
		r := cue.Result{
			CueName:  "app",
			Nature:   "pack",
			Status:   status,
			Warnings: []string{"2 staged file(s) not committed — will NOT be deployed: foo.go, bar.go"},
		}
		lines := output.CueDetailLines(r, false, false, nil)
		var found bool
		for _, l := range lines {
			if strings.Contains(l, "⚠") && strings.Contains(l, "staged") {
				found = true
			}
		}
		if !found {
			t.Errorf("status=%v: expected warning with ⚠ in detail lines, got %v", status, lines)
		}
	}
}

func TestCueDetailLines_equalWithStdout_shown(t *testing.T) {
	r := cue.Result{
		CueName: "app",
		Nature:  "pack",
		Status:  cue.StatusEqual,
		Stdout:  "commit abc1234",
	}
	lines := output.CueDetailLines(r, false, false, nil)
	var found bool
	for _, l := range lines {
		if strings.Contains(l, "abc1234") {
			found = true
		}
	}
	if !found {
		t.Errorf("StatusEqual stdout must appear in detail lines, got %v", lines)
	}
}

func TestCueDetailLines_diffShownWhenRequested(t *testing.T) {
	r := cue.Result{
		CueName: "cfg",
		Nature:  "config",
		Status:  cue.StatusChanged,
		Diff:    "--- a\n+++ b\n@@ -1 +1 @@\n-old\n+new\n",
	}
	lines := output.CueDetailLines(r, true, false, nil)
	found := false
	for _, l := range lines {
		if strings.Contains(l, "+new") {
			found = true
		}
	}
	if !found {
		t.Errorf("diff must appear in CueDetailLines when showDiff=true; got %v", lines)
	}
}
