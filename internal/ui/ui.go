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

// The 256-colour palette, one tone per marker, matching the muted style
// gohelp-luar already uses for its own output (dim, and 38;5;<n> for
// anything beyond the basic 8). errorTone and warningTone double as the
// tones for message output, per concept.md "Listing output": "'!' and '?'
// borrow the tones errors and warnings already use everywhere else."
const (
	dim         = "\033[2m"
	green       = "\033[38;5;108m"
	purple      = "\033[38;5;139m"
	errorTone   = "\033[38;5;167m"
	warningTone = "\033[38;5;179m"
	reset       = "\033[0m"
)

var markerColor = map[string]string{
	MarkerEnabled:      green,
	MarkerMaterialized: purple,
	MarkerAbsent:       dim,
	MarkerProblem:      errorTone,
	MarkerUntracked:    warningTone,
}

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

// colorEnabled reports whether output to f should carry colour: only ever
// when f is a terminal, and never when NO_COLOR is set, regardless of
// terminal. Piped output stays plain text so listings stay parseable.
func colorEnabled(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return isTerminal(f)
}

// Render formats entries for the listing output. Success prints nothing —
// callers simply skip printing when entries is empty. Each marker carries
// its own tone from concept.md's palette, applied to the whole line, only
// when writing to a terminal with NO_COLOR unset.
func Render(entries []Entry) string {
	colored := colorEnabled(os.Stdout)
	var b strings.Builder
	for _, e := range entries {
		line := fmt.Sprintf("%s %s", e.Marker, e.Name)
		if colored {
			if color, ok := markerColor[e.Marker]; ok {
				line = color + line + reset
			}
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// ErrorTone wraps msg in the error tone used for "!" markers, for message
// output that should read as the same kind of problem, per concept.md
// "Listing output". Coloured only when stderr is a terminal and NO_COLOR is
// unset.
func ErrorTone(msg string) string {
	if !colorEnabled(os.Stderr) {
		return msg
	}
	return errorTone + msg + reset
}

// WarningTone wraps msg in the warning tone used for "?" markers, for
// message output that should read as the same kind of problem, per
// concept.md "Listing output". Coloured only when stderr is a terminal and
// NO_COLOR is unset.
func WarningTone(msg string) string {
	if !colorEnabled(os.Stderr) {
		return msg
	}
	return warningTone + msg + reset
}

// Prompt asks the user to choose among options, returning their raw
// response. Options are rendered inline in the conventional "(y/N)" form,
// with the capitalized one signalling the default, and a blank line ahead
// of the question separates it from whatever listing preceded it. Passing
// no options asks for free-text instead. Callers are responsible for
// validating the choice. It is an error to call this when Interactive() is
// false.
func Prompt(message string, options []string) (string, error) {
	if !Interactive() {
		return "", fmt.Errorf("cannot prompt: not an interactive session")
	}
	question := message
	if len(options) > 0 {
		question = fmt.Sprintf("%s (%s)", message, strings.Join(options, "/"))
	}
	fmt.Fprintf(os.Stderr, "\n%s ", question)

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	return strings.TrimSpace(line), nil
}
