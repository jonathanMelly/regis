// cmd/regis/cmd/fetch_test.go
package cmd_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"git.disroot.org/jmy/regis/cmd/regis/cmd"
	"git.disroot.org/jmy/regis/internal/config"
)

// --- FetchLocalPath ---

func TestFetchLocalPath_render_with_local_dest(t *testing.T) {
	cr := config.CueRef{Nature: "render", LocalDest: "nginx/generated.conf"}
	got := cmd.FetchLocalPath(cr)
	if got != "nginx/generated.conf" {
		t.Errorf("want nginx/generated.conf, got %q", got)
	}
}

func TestFetchLocalPath_render_without_local_dest(t *testing.T) {
	cr := config.CueRef{Nature: "render"}
	got := cmd.FetchLocalPath(cr)
	if got != "" {
		t.Errorf("want empty, got %q", got)
	}
}

func TestFetchLocalPath_binary_with_src(t *testing.T) {
	cr := config.CueRef{Nature: "binary", Src: config.StringOrList{"bin/myapp"}}
	got := cmd.FetchLocalPath(cr)
	if got != "bin/myapp" {
		t.Errorf("want bin/myapp, got %q", got)
	}
}

func TestFetchLocalPath_binary_without_src(t *testing.T) {
	cr := config.CueRef{Nature: "binary"}
	got := cmd.FetchLocalPath(cr)
	if got != "" {
		t.Errorf("want empty, got %q", got)
	}
}

func TestFetchLocalPath_secret_uses_first_src(t *testing.T) {
	cr := config.CueRef{Nature: "secret", Src: config.StringOrList{".env.server", ".env.extra"}}
	got := cmd.FetchLocalPath(cr)
	if got != ".env.server" {
		t.Errorf("want .env.server, got %q", got)
	}
}

// --- RunReverseShell ---

func TestRunReverseShell_executes_with_artifact_path(t *testing.T) {
	// Verify ARTIFACT_PATH is injected and the shell can use it.
	tmpDir := t.TempDir()
	artifactPath := filepath.Join(tmpDir, "fetched.conf")
	markerPath := filepath.Join(tmpDir, "marker.txt")

	var shell string
	if runtime.GOOS == "windows" {
		shell = `[IO.File]::WriteAllText('` + markerPath + `', $env:ARTIFACT_PATH)`
	} else {
		shell = `printf '%s' "$ARTIFACT_PATH" > '` + markerPath + `'`
	}

	if err := cmd.RunReverseShell(shell, artifactPath); err != nil {
		t.Fatalf("RunReverseShell failed: %v", err)
	}

	got, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("marker not written: %v", err)
	}
	if string(got) != artifactPath {
		t.Errorf("ARTIFACT_PATH = %q, want %q", string(got), artifactPath)
	}
}

func TestRunReverseShell_failure_returns_error(t *testing.T) {
	err := cmd.RunReverseShell("exit 1", "/tmp/any")
	if err == nil {
		t.Error("expected error from failing shell, got nil")
	}
}
