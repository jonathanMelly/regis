// cmd/regis/cmd/release_test.go
package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.disroot.org/jmy/regis/internal/config"
)

func TestHashesEqual_identical(t *testing.T) {
	a := map[string]string{"bin": "aabb", "cfg": "ccdd"}
	b := map[string]string{"bin": "aabb", "cfg": "ccdd"}
	if !hashesEqual(a, b) { t.Error("expected equal") }
}

func TestHashesEqual_valueDiffer(t *testing.T) {
	a := map[string]string{"bin": "aabb"}
	b := map[string]string{"bin": "xxxx"}
	if hashesEqual(a, b) { t.Error("expected not equal") }
}

func TestHashesEqual_lengthDiffer(t *testing.T) {
	a := map[string]string{"bin": "aabb", "cfg": "ccdd"}
	b := map[string]string{"bin": "aabb"}
	if hashesEqual(a, b) { t.Error("expected not equal when lengths differ") }
}

func TestHashesEqual_bothNil(t *testing.T) {
	if !hashesEqual(nil, nil) { t.Error("two nil maps should be equal") }
}

func TestEffectiveLocalDir_default(t *testing.T) {
	cfg := &config.Config{}
	if got := effectiveStateDir(cfg); got != ".regis-states" {
		t.Errorf("want .regis-states, got %q", got)
	}
}

func TestEffectiveLocalDir_custom(t *testing.T) {
	cfg := &config.Config{State: config.StateConfig{LocalDir: "/tmp/my-states"}}
	if got := effectiveStateDir(cfg); got != "/tmp/my-states" {
		t.Errorf("want /tmp/my-states, got %q", got)
	}
}

func TestShortHash_long(t *testing.T) {
	if got := shortHash("aabbccdd11223344"); got != "aabbccdd" {
		t.Errorf("want first 8 chars, got %q", got)
	}
}

func TestShortHash_short(t *testing.T) {
	if got := shortHash("abc"); got != "abc" {
		t.Errorf("short string should pass through, got %q", got)
	}
}

func TestLatestLocalSnapshot_returnsNewest(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"v20260610-120000", "v20260612-090000", "v20260611-150000"} {
		snap := filepath.Join(dir, "maisondumouvement")
		os.MkdirAll(snap, 0755)
		os.WriteFile(filepath.Join(snap, id+".yml"), []byte("id: "+id), 0644)
	}
	got := latestLocalStateFile(dir)
	if !strings.Contains(got, "v20260612-090000") {
		t.Errorf("want latest snapshot (v20260612-090000), got %q", got)
	}
}

func TestLatestLocalSnapshot_empty(t *testing.T) {
	if got := latestLocalStateFile(t.TempDir()); got != "" {
		t.Errorf("want empty string for empty dir, got %q", got)
	}
}
