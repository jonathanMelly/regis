// internal/output/level.go
package output

import (
	"os"

	"golang.org/x/term"
)

// Level controls output richness.
type Level int

const (
	Level1 Level = 1 // plain — CI / no-TTY / NO_COLOR
	Level2 Level = 2 // full color + live TUI (any TTY)

	// Level3 is kept as an alias for Level2 for backwards compatibility.
	Level3 = Level2
)

// DetectLevel auto-detects the appropriate output level.
//
// Overrides (checked in order):
//   - NO_COLOR or REGIS_PLAIN=1  → Level1
//   - REGIS_LEVEL=1|2            → explicit level
//   - no TTY on stdout           → Level1
//   - otherwise                  → Level2 (TUI)
func DetectLevel() Level {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("REGIS_PLAIN") != "" {
		return Level1
	}
	switch os.Getenv("REGIS_LEVEL") {
	case "1":
		return Level1
	case "2", "3":
		return Level2
	}
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return Level1
	}
	return Level2
}
