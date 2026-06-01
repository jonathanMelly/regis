// internal/runner/prepost_test.go
package runner

import (
	"context"
	"testing"

	"git.disroot.org/jmy/regis/internal/config"
)

// stubConn satisfies runPrePost's conn interface (never called for local=true).
type stubConn struct{}

func (s *stubConn) Run(cmd string) (string, string, int, error) {
	return "", "", 0, nil
}

func TestRunPrePost_local_success(t *testing.T) {
	pp := config.PrePost{Cmd: "true", Local: true}
	if err := runPrePost(context.Background(), pp, &stubConn{}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunPrePost_local_failure(t *testing.T) {
	pp := config.PrePost{Cmd: "false", Local: true}
	if err := runPrePost(context.Background(), pp, &stubConn{}); err == nil {
		t.Error("expected error for failing local command, got nil")
	}
}

func TestRunPrePost_remote_success(t *testing.T) {
	pp := config.PrePost{Cmd: "uptime", Local: false}
	conn := &stubConn{}
	if err := runPrePost(context.Background(), pp, conn); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSingleQuote_noSpecial(t *testing.T) {
	if got := singleQuote("/opt/app/.regis.lock"); got != "'/opt/app/.regis.lock'" {
		t.Errorf("unexpected: %s", got)
	}
}

func TestSingleQuote_embeddedSingleQuote(t *testing.T) {
	// it's → 'it'\''s'
	if got := singleQuote("it's"); got != `'it'\''s'` {
		t.Errorf("unexpected: %s", got)
	}
}
