// internal/output/spinner.go
package output

import (
	"fmt"
	"sync"
	"time"
)

// Spinner is a simple in-place terminal progress indicator that writes to stdout.
// For Level1 (plain/CI), all methods are no-ops.
// Safe for concurrent Update calls.
type Spinner struct {
	mu      sync.Mutex
	msg     string
	stopCh  chan struct{}
	doneCh  chan struct{}
	stopped bool
	level   Level
}

// NewSpinner returns a Spinner for the given output level and initial message.
// Level1 returns a no-op spinner (safe to call, does nothing).
func NewSpinner(level Level, msg string) *Spinner {
	return &Spinner{
		level:  level,
		msg:    msg,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

// Start begins the spinner animation on a background goroutine.
// No-op for Level1.
func (s *Spinner) Start() {
	if s.level < Level2 {
		close(s.doneCh)
		return
	}
	frames := []byte{'-', '\\', '|', '/'}
	go func() {
		defer close(s.doneCh)
		i := 0
		for {
			select {
			case <-s.stopCh:
				fmt.Print("\r\x1b[K") // clear line
				return
			case <-time.After(100 * time.Millisecond):
				s.mu.Lock()
				msg := s.msg
				s.mu.Unlock()
				fmt.Printf("\r\x1b[K  %c  %s", frames[i%4], msg)
				i++
			}
		}
	}()
}

// Update replaces the spinner message. Safe for concurrent use.
// No-op for Level1.
func (s *Spinner) Update(msg string) {
	if s.level < Level2 {
		return
	}
	s.mu.Lock()
	s.msg = msg
	s.mu.Unlock()
}

// Stop halts the spinner and clears its line from stdout.
// Blocks until the background goroutine has exited and the line is cleared.
func (s *Spinner) Stop() {
	if s.level < Level2 {
		return
	}
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		<-s.doneCh
		return
	}
	s.stopped = true
	s.mu.Unlock()
	close(s.stopCh)
	<-s.doneCh
}
