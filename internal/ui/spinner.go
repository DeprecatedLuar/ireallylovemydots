package ui

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// spinnerInterval is how long each spinner frame shows.
const spinnerInterval = 120 * time.Millisecond

// clearLine returns to column 0 and erases the line.
const clearLine = "\r\033[2K"

var (
	spinnerFrames = []string{"|", "/", "-", "\\"}
	spinnerTones  = []string{errorTone, warningTone, green, blue}
)

// Spinner draws a frame and label on stdout's current line until Stop. A
// nil *Spinner does nothing.
type Spinner struct {
	done    chan struct{}
	cleared chan struct{}
	once    sync.Once
}

// StartSpinner starts a spinner labelled label, or returns nil when stdout
// is not a terminal.
func StartSpinner(label string) *Spinner {
	if !isTerminal(os.Stdout) {
		return nil
	}
	s := newSpinner()
	go s.run(label, colorEnabled(os.Stdout))
	return s
}

func newSpinner() *Spinner {
	return &Spinner{done: make(chan struct{}), cleared: make(chan struct{})}
}

func (s *Spinner) run(label string, color bool) {
	defer close(s.cleared)
	ticker := time.NewTicker(spinnerInterval)
	defer ticker.Stop()
	for i := 0; ; i = (i + 1) % len(spinnerFrames) {
		frame := spinnerFrames[i]
		if color {
			frame = spinnerTones[i] + frame + reset
		}
		fmt.Print(clearLine + frame + " " + label)
		select {
		case <-s.done:
			fmt.Print(clearLine)
			return
		case <-ticker.C:
		}
	}
}

// Stop erases the spinner's line and returns once nothing more will be
// drawn, so the next output starts on a clean line.
func (s *Spinner) Stop() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		close(s.done)
		<-s.cleared
	})
}
