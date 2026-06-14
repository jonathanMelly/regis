// internal/runner/compensate_ui.go
package runner

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"git.disroot.org/jmy/regis/internal/config"
	"golang.org/x/term"
)

// errCompensationStopped is returned when the operator chooses to stop all compensations.
var errCompensationStopped = errors.New("compensation stopped by operator")

// CompensateChoice is the operator's response to an interactive compensation prompt.
type CompensateChoice int

const (
	CompensateRun   CompensateChoice = iota // run the suggested command
	CompensateShell                          // open an interactive remote shell
	CompensateSkip                           // skip this compensation
	CompensateStop                           // stop all compensations
)

// CompensateUI drives the interactive compensation prompts.
// A nil UI auto-runs all compensations without prompting (CI / non-interactive mode).
type CompensateUI interface {
	// Prompt shows the suggested command and returns the operator's choice.
	// When shell is empty (compensation: interactive), CompensateRun is never returned.
	Prompt(cueName, shell string) CompensateChoice
	// OpenShell opens an interactive remote session on the target.
	OpenShell(target config.Target) error
}

// IsTTY reports whether stdin and stdout are connected to a terminal.
func IsTTY() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

// NewTTYCompensateUI returns a terminal-driven CompensateUI.
// Call only when IsTTY() is true.
func NewTTYCompensateUI() CompensateUI { return &ttyCompensateUI{} }

type ttyCompensateUI struct{}

func (u *ttyCompensateUI) Prompt(cueName, shell string) CompensateChoice {
	fmt.Println()
	fmt.Printf("● compensation triggered for: %s\n", cueName)
	if shell != "" {
		fmt.Printf("  suggested: %s\n", shell)
		fmt.Println()
		fmt.Print("  [enter] run   [s] open shell   [k] skip   [q] stop all\n  > ")
	} else {
		fmt.Println()
		fmt.Print("  [s] open shell   [k] skip   [q] stop all\n  > ")
	}

	r := bufio.NewReader(os.Stdin)
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))

	switch line {
	case "", "y", "yes":
		if shell == "" {
			// no command to run — open shell instead
			return CompensateShell
		}
		return CompensateRun
	case "s":
		return CompensateShell
	case "k":
		fmt.Printf("  skipping compensation for %s\n", cueName)
		return CompensateSkip
	case "q":
		fmt.Println("  stopping all compensations")
		return CompensateStop
	default:
		fmt.Printf("  unknown input %q — stopping all compensations\n", line)
		return CompensateStop
	}
}

func (u *ttyCompensateUI) OpenShell(target config.Target) error {
	host := target.Host
	port := target.Port
	if port == "" {
		port = "22"
	}
	user := target.User

	fmt.Printf("\n  opening shell on %s@%s — type 'exit' when done\n\n", user, host)

	args := []string{"-p", port, fmt.Sprintf("%s@%s", user, host)}
	// Request pseudo-terminal so interactive commands (mysql, etc.) work.
	args = append([]string{"-t"}, args...)

	cmd := exec.Command("ssh", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		// ssh exits non-zero when the user does 'exit 1' or connection drops —
		// treat as informational; the operator decides what happened.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			fmt.Printf("\n  shell exited with code %s\n", strconv.Itoa(exitErr.ExitCode()))
			return nil
		}
		return fmt.Errorf("open shell: %w", err)
	}
	return nil
}
