// Package ui holds terminal I/O: TTY detection, prompts, and the single
// listing renderer shared by every "list" call, whatever it is scoped to.
package ui

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Markers used by the listing renderer. One character of state per line,
// nothing else — see concept.md "Listing output".
const (
	MarkerEnabled      = "+" // enabled
	MarkerMaterialized = "-" // materialized, not linked
	MarkerProblem      = "!" // drift, conflict, or unwritable destination
	MarkerAbsent       = "=" // in the repository, not on this machine (dim)
	MarkerUntracked    = "?" // payload with no manifest entry
)

const dim = "\033[2m"
const reset = "\033[0m"

// Entry is one rendered line: a marker and the name it applies to.
type Entry struct {
	Marker string
	Name   string
}

// Interactive reports whether both stdin and stdout are attached to a
// terminal. Ambiguity prompts only when this is true; otherwise callers
// must error with the candidate list instead.
func Interactive() bool {
	return isTerminal(os.Stdin) && isTerminal(os.Stdout)
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// Render formats entries for the listing output. Success prints nothing —
// callers simply skip printing when entries is empty. `=` lines are dimmed
// when connected to a terminal; `!` lines are never dimmed.
func Render(entries []Entry) string {
	dimmable := isTerminal(os.Stdout)
	var b strings.Builder
	for _, e := range entries {
		line := fmt.Sprintf("%s %s", e.Marker, e.Name)
		if dimmable && e.Marker == MarkerAbsent {
			line = dim + line + reset
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// Prompt asks the user to choose among options, returning their raw
// response. Callers are responsible for validating the choice. It is an
// error to call this when Interactive() is false.
func Prompt(message string, options []string) (string, error) {
	if !Interactive() {
		return "", fmt.Errorf("cannot prompt: not an interactive session")
	}
	fmt.Fprintln(os.Stderr, message)
	for _, opt := range options {
		fmt.Fprintf(os.Stderr, "  %s\n", opt)
	}
	fmt.Fprint(os.Stderr, "> ")

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	return strings.TrimSpace(line), nil
}
