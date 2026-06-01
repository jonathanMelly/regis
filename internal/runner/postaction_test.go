// internal/runner/postaction_test.go
package runner_test

import (
	"testing"
	"git.disroot.org/jmy/regis/internal/cue"
	"git.disroot.org/jmy/regis/internal/runner"
)

func TestDeduplicatePostActions_sameCmd(t *testing.T) {
	actions := []cue.PostAction{
		{Cmd: "nginx -t && nginx -s reload", Sudo: false},
		{Cmd: "nginx -t && nginx -s reload", Sudo: true}, // sudo wins
		{Cmd: "restart saver", Sudo: false},
	}
	got := runner.DeduplicatePostActions(actions)
	if len(got) != 2 {
		t.Fatalf("want 2 deduplicated actions, got %d: %v", len(got), got)
	}
	// First action should have sudo=true (most permissive wins)
	if !got[0].Sudo {
		t.Error("expected sudo=true for nginx reload (most permissive)")
	}
}

func TestDeduplicatePostActions_preservesOrder(t *testing.T) {
	actions := []cue.PostAction{
		{Cmd: "stop saver"},
		{Cmd: "start saver"},
	}
	got := runner.DeduplicatePostActions(actions)
	if len(got) != 2 || got[0].Cmd != "stop saver" {
		t.Errorf("order not preserved: %v", got)
	}
}
