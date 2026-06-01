// internal/output/level.go
package output

import (
	"os"
	"strings"

	"golang.org/x/term"
)

// Level controls output richness.
type Level int

const (
	Level1 Level = 1 // plain — CI / no-TTY / NO_COLOR
	Level2 Level = 2 // basic color (16 colors)
	Level3 Level = 3 // full color + live in-place updates
)

// DetectLevel auto-detects the appropriate output level.
//
// Overrides (checked in order):
//   - NO_COLOR or REGIS_PLAIN=1    → Level1
//   - REGIS_LEVEL=1|2|3            → explicit level
//   - no TTY on stdout             → Level1
//   - WT_SESSION (Windows Terminal) → Level3
//   - COLORTERM=truecolor|24bit     → Level3
//   - TERM=xterm-256color or tmux* or screen* → Level3
//   - TERM=xterm* or rxvt*         → Level2
//   - otherwise                    → Level2
func DetectLevel() Level {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("REGIS_PLAIN") != "" {
		return Level1
	}
	switch os.Getenv("REGIS_LEVEL") {
	case "1":
		return Level1
	case "2":
		return Level2
	case "3":
		return Level3
	}
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return Level1
	}
	// Windows Terminal sets WT_SESSION — full TUI support.
	if os.Getenv("WT_SESSION") != "" {
		return Level3
	}
	// COLORTERM=truecolor|24bit — explicit true-color signal (common on Linux/Mac).
	colorterm := os.Getenv("COLORTERM")
	if colorterm == "truecolor" || colorterm == "24bit" {
		return Level3
	}
	// TERM with 256color or multiplexers → full color and TUI capable.
	termenv := os.Getenv("TERM")
	if strings.HasSuffix(termenv, "256color") ||
		strings.HasPrefix(termenv, "tmux") ||
		strings.HasPrefix(termenv, "screen") {
		return Level3
	}
	// Plain xterm or rxvt → 16-color, no live updates.
	if strings.HasPrefix(termenv, "xterm") || strings.HasPrefix(termenv, "rxvt") {
		return Level2
	}
	return Level2
}
