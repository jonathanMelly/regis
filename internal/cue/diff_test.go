// internal/cue/diff_test.go
package cue_test

import (
	"strings"
	"testing"
	"git.disroot.org/jmy/regis/internal/cue"
)

func TestTextDiff_changed(t *testing.T) {
	local := "upstream backend { server 10.0.0.1; }\n"
	remote := "upstream backend { server 10.0.0.2; }\n"
	diff, changed := cue.TextDiff(local, remote, "remote", "local")
	if !changed {
		t.Error("want changed=true")
	}
	if !strings.Contains(diff, "-") || !strings.Contains(diff, "+") {
		t.Errorf("expected unified diff markers, got:\n%s", diff)
	}
}

func TestTextDiff_unchanged(t *testing.T) {
	s := "same content\n"
	_, changed := cue.TextDiff(s, s, "remote", "local")
	if changed {
		t.Error("want changed=false for identical content")
	}
}

func TestSecretDiff_masksValues(t *testing.T) {
	local := "TOKEN=abc123\nOTHER=hello\n"
	remote := "TOKEN=old\nOTHER=hello\n"
	diff, changed := cue.SecretDiff(local, remote, nil)
	if !changed {
		t.Error("want changed=true")
	}
	if strings.Contains(diff, "abc123") || strings.Contains(diff, "old") {
		t.Errorf("diff must not contain secret values:\n%s", diff)
	}
	if !strings.Contains(diff, "TOKEN") {
		t.Errorf("diff should show key names:\n%s", diff)
	}
}

func TestSecretDiff_preserve(t *testing.T) {
	local := "TOKEN=new\nKEEP=mine\n"
	remote := "TOKEN=old\nKEEP=server-value\n"
	_, merged := cue.MergeSecrets(local, remote, []string{"KEEP"})
	if !strings.Contains(merged, "KEEP=server-value") {
		t.Errorf("preserved key overwritten; merged:\n%s", merged)
	}
	if !strings.Contains(merged, "TOKEN=new") {
		t.Errorf("non-preserved key not updated; merged:\n%s", merged)
	}
}
