//go:build windows

// cmd/regis/cmd/console_windows.go
package cmd

import (
	"os"

	"golang.org/x/sys/windows"
)

func init() {
	// Enable ANSI/VT100 escape sequence processing on Windows console.
	// Required for ConEmu, Windows Terminal, and cmd.exe (Win10+) to render
	// colors and box-drawing characters correctly.
	for _, f := range []*os.File{os.Stdout, os.Stderr} {
		handle := windows.Handle(f.Fd())
		var mode uint32
		if windows.GetConsoleMode(handle, &mode) == nil {
			windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
		}
	}
}
