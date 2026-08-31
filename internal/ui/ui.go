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
	// MarkerRemoved appears only in the output of a mutation, never in a
	// listing: the namespace was removed and has no state left to report,
	// per concept.md "Listing output".
	MarkerRemoved = "x"
)

// The 256-colour palette, one tone per marker, matching the muted style
// gohelp-luar already uses for its own output (dim, and 38;5;<n> for
// anything beyond the basic 8). errorTone and warningTone double as the
// tones for message output, per concept.md "Listing output": "'!' and '?'
// borrow the tones errors and warnings already use everywhere else."
const (
	dim         = "\033[2m"
	bold        = "\033[1m"
	green       = "\033[38;5;108m"
	purple      = "\033[38;5;139m"
	errorTone   = "\033[38;5;167m"
	warningTone = "\033[38;5;179m"
	cyanTone    = "\033[38;5;80m" // the one standard tone for a closing tip, everywhere a tip is printed
	reset       = "\033[0m"
)

var markerColor = map[string]string{
	MarkerEnabled:      green,
	MarkerMaterialized: purple,
	MarkerAbsent:       dim,
	MarkerProblem:      errorTone,
	MarkerUntracked:    warningTone,
	MarkerRemoved:      errorTone,
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

// colorLine wraps line in marker's tone when f is a colour-enabled
// destination, per concept.md "Listing output": the marker and its line
// share one tone. Unknown markers (and any tone-less caller) pass through
// unchanged.
func colorLine(marker, line string, f *os.File) string {
	if !colorEnabled(f) {
		return line
	}
	if color, ok := markerColor[marker]; ok {
		return color + line + reset
	}
	return line
}

// tipTone wraps a closing tip in the one standard tip colour, so every tip
// printed anywhere in dots reads the same way, regardless of what kind of
// message (confirmation, error) it closes. Coloured only when f is a
// colour-enabled destination.
func tipTone(tip string, f *os.File) string {
	if tip == "" || !colorEnabled(f) {
		return tip
	}
	return bold + cyanTone + tip + reset
}

// dimTone wraps s in the dim tone already used for MarkerAbsent, for
// secondary detail that should read as quieter than the line it's on.
// Coloured only when f is a colour-enabled destination.
func dimTone(s string, f *os.File) string {
	if s == "" || !colorEnabled(f) {
		return s
	}
	return dim + s + reset
}

// Render formats entries for the listing output. Success prints nothing —
// callers simply skip printing when entries is empty. Each marker carries
// its own tone from concept.md's palette, applied to the whole line, only
// when writing to a terminal with NO_COLOR unset.
func Render(entries []Entry) string {
	var b strings.Builder
	for _, e := range entries {
		line := colorLine(e.Marker, fmt.Sprintf("%s %s", e.Marker, e.Name), os.Stdout)
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// DetailSep separates a mutation report line's name from its trailing
// detail (a destination, or what occupies it) — two spaces reads as a
// column break without pretending to be alignment, since these lines are
// read one at a time rather than scanned as an aligned block. Exported so
// any caller building its own detail string (e.g. a pre-flight skip
// summary combining a destination and what occupies it) uses the exact
// same column shape as Operation/Sub do internally.
const DetailSep = "    "

// Operation formats one line reporting a mutation's resulting state, per
// concept.md "What enable reports": the marker for the namespace's state
// after the operation, its name, and an optional trailing detail (what it
// could not do, and why). detail may be empty. Every command that changes a
// namespace's state — enable, disable, install, uninstall, rm — reports
// through this so there is one line shape across all of them.
func Operation(marker, name, detail string) string {
	line := fmt.Sprintf("%s %s", marker, name)
	if detail != "" {
		line += DetailSep + detail
	}
	return colorLine(marker, line, os.Stdout) + "\n"
}

// subIndent marks a Sub line as belonging to the Operation line above it —
// paths are not namespaces and never take a marker line of their own, per
// concept.md "What enable reports": "paths are not namespaces and do not
// take a marker line of their own."
const subIndent = "  "

// Sub formats an indented sub-line under an Operation line, reporting what
// happened to a path underneath it — a trashed occupant, most commonly.
// detail may be empty.
func Sub(marker, path, detail string) string {
	line := subIndent + fmt.Sprintf("%s %s", marker, path)
	if detail != "" {
		line += DetailSep + detail
	}
	return colorLine(marker, line, os.Stdout) + "\n"
}

// Report joins already-formatted Operation/Sub lines (each carrying its own
// trailing newline) into one report, with an optional footer paragraph —
// concept.md's count line ("24 enabled, 2 skipped...") — separated by a
// blank line. Callers that must keep the count line off stdout (concept.md:
// "It goes to stderr, so the marker lines stay pipeable on their own")
// render it separately instead of passing it here.
func Report(lines []string, footer string) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l)
	}
	if footer != "" {
		b.WriteString("\n")
		b.WriteString(footer)
		b.WriteString("\n")
	}
	return b.String()
}

// List formats a header, a blank line, one item per line indented under it,
// a blank line, and an optional closing tip — for reporting an actual list
// of things (as opposed to Confirm's single summary line under a prompt).
// tip may be empty.
func List(header string, items []string, tip string) string {
	var b strings.Builder
	b.WriteString(header)
	if len(items) > 0 {
		b.WriteString("\n\n")
		for i, item := range items {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString("  ")
			b.WriteString(item)
		}
	}
	if tip != "" {
		b.WriteString("\n\n")
		b.WriteString(tipTone(tip, os.Stderr))
	}
	return b.String()
}

// Pair is one name/value line for RenderAligned.
type Pair struct {
	Name  string
	Value string
}

// Arrow is the one arrow character dots uses wherever output shows a
// subject going to an outcome — a real arrow, never a listing marker, so it
// never borrows "+"/"-"/"!"/"=" and implies state that does not exist yet
// (concept.md "Bootstrap"). RenderAligned's columns and confirmRemoval's
// scale line both read off this same glyph.
const Arrow = "→"

// Plural renders n and word as a count label, pluralizing word with a
// trailing "s" when n != 1 — "1 file", "6 files". word must already be
// singular; this only covers dots' own vocabulary (file, namespace, repo),
// none of which pluralize irregularly.
func Plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// RenderAligned formats pairs as two aligned columns, name then value,
// joined by a real arrow: the one reusable column formatter for anything
// pairing a name with a value outside a listing (concept.md's bootstrap
// preview is the first case). The column width is computed from the widest
// name in a single pass; callers never compute it themselves. Listings do
// not use this — the marker is the message there, so alignment would imply
// a state that is not being reported.
func RenderAligned(pairs []Pair) string {
	width := 0
	for _, p := range pairs {
		if len(p.Name) > width {
			width = len(p.Name)
		}
	}

	var b strings.Builder
	for _, p := range pairs {
		fmt.Fprintf(&b, "%-*s %s %s\n", width, p.Name, Arrow, p.Value)
	}
	return b.String()
}

// CountedItems formats pairs as name, then a parenthesized count in the dim
// tone, with every "(" aligned to the same column — for List callers that
// need to show each named item's size (confirmRemoval's namespace/item
// counts is the first case). Value is the text inside the parens (e.g.
// "6 items"); a pair with an empty Value is rendered as its bare Name, not
// padded, since it has nothing to align against. The column width is
// computed from the widest Name among pairs that do carry a Value.
func CountedItems(pairs []Pair) []string {
	width := 0
	for _, p := range pairs {
		if p.Value != "" && len(p.Name) > width {
			width = len(p.Name)
		}
	}

	lines := make([]string, len(pairs))
	for i, p := range pairs {
		if p.Value == "" {
			lines[i] = p.Name
			continue
		}
		paren := dimTone(fmt.Sprintf("(%s)", p.Value), os.Stderr)
		lines[i] = fmt.Sprintf("%-*s %s", width, p.Name, paren)
	}
	return lines
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

// Prompt asks question, with options rendered inline in the conventional
// "(y/N)" form (the capitalized one signalling the default), and returns
// the raw response. context is optional leading material — a List or
// RenderAligned block, say — printed above question, blank-line separated,
// so options always attach to the actual question and never run into
// whatever the block's last line happened to be; pass "" when there is no
// such block. Passing no options asks for free-text instead. Callers are
// responsible for validating the choice. It is an error to call this when
// Interactive() is false.
func Prompt(context, question string, options []string) (string, error) {
	if !Interactive() {
		return "", fmt.Errorf("cannot prompt: not an interactive session")
	}
	if len(options) > 0 {
		question = fmt.Sprintf("%s (%s)", question, strings.Join(options, "/"))
	}
	msg := question
	if context != "" {
		msg = context + "\n\n" + question
	}
	fmt.Fprintf(os.Stderr, "\n%s ", msg)

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	return strings.TrimSpace(line), nil
}
