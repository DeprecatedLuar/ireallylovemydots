// Package ui holds terminal I/O: TTY detection, prompts, and the single
// listing renderer shared by every "list" call, whatever it is scoped to.
package ui

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/DeprecatedLuar/dotz/internal/manifest"
)

// Markers used by the listing renderer. One character of state per line,
// nothing else — see concept.md "Listing output".
const (
	MarkerEnabled      = "+" // enabled
	MarkerMaterialized = "-" // materialized, not linked
	MarkerProblem      = "!" // every finding: orphan, invalid entry, untracked payload, profile drift
	MarkerAbsent       = "=" // in the repository, not on this machine (dim)
	// MarkerRemoved appears only in the output of a mutation, never in a
	// listing: the namespace was removed and has no state left to report,
	// per concept.md "Listing output".
	MarkerRemoved = "x"
)

// The basic 8-colour palette, one tone per marker: the standard SGR codes,
// so each tone renders through whatever colours the user's terminal theme
// maps them to, rather than a fixed 256-colour RGB that looks the same
// everywhere. errorTone and warningTone double as the tones for message
// output, per concept.md "Listing output": "'!' and '?' borrow the tones
// errors and warnings already use everywhere else."
const (
	dim         = "\033[2m"
	green       = "\033[32m"
	purple      = "\033[35m"
	blue        = "\033[34m"
	errorTone   = "\033[31m"
	warningTone = "\033[33m"
	reset       = "\033[0m"
)

// MarkerProblem carries warningTone rather than errorTone: "!" marks
// something a human has to look at, not something that failed, so red is
// reserved for a marker that means an operation did not do what it was
// asked — MarkerRemoved, and the arrow die() prints on a fatal error.
var markerColor = map[string]string{
	MarkerEnabled:      green,
	MarkerMaterialized: purple,
	MarkerAbsent:       dim,
	MarkerProblem:      warningTone,
	MarkerRemoved:      errorTone,
}

// Entry is one rendered line: a marker and the name it applies to. Count is
// optional (0 = none) — a caller that must convey blast radius, such as
// rm's confirmation, sets it and gets an aligned "(n items)" column; a bare
// listing never does, per concept.md "Listing output". Repo is optional
// ("" = none) — a caller that found this Name colliding across
// repositories in the rendered set qualifies the row with it, per
// concept.md "Listing output": "A name carried by two repositories is
// qualified, and only then." Profile is optional ("" = main, no bracket) —
// the namespace's active profile, rendered as "[<name>]" between Name and
// the repository qualifier, per concept.md "Listing output": "A namespace
// with a profile active carries its name in brackets."
type Entry struct {
	Marker  string
	Name    string
	Count   int
	Repo    string
	Profile string
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

// tipTone wraps a closing tip in the same dim tone used for secondary
// detail elsewhere (dimTone), so every tip printed anywhere in dots reads
// as quieter than the line it closes, regardless of what kind of message
// (confirmation, error) it closes. Coloured only when f is a
// colour-enabled destination.
func tipTone(tip string, f *os.File) string {
	return dimTone(tip, f)
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

// blueTone wraps s in blue, the one tone in the palette carried by neither
// a marker nor the dim qualifier — concept.md "Listing output": "Blue is
// the only tone added past the marker set, and it is added for an
// annotation rather than a marker." Coloured only when f is a
// colour-enabled destination.
func blueTone(s string, f *os.File) string {
	if s == "" || !colorEnabled(f) {
		return s
	}
	return blue + s + reset
}

// Render formats entries for the listing output. Success prints nothing —
// callers simply skip printing when entries is empty. Each marker carries
// its own tone from concept.md's palette, applied to the whole line, only
// when writing to a terminal with NO_COLOR unset.
func Render(entries []Entry) string {
	var b strings.Builder
	for _, line := range RenderLines(entries, os.Stdout) {
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// RenderLines formats entries one line per entry, without a trailing
// newline, for a caller that embeds them in a larger block (rm's
// confirmation, via List) instead of printing them directly. f is the
// destination the block will actually be written to, since colour and
// NO_COLOR are decided per-destination, not always stdout.
//
// When any entry carries a Count or a Repo, every such entry's line gets an
// aligned trailing-parenthesis column in the dim tone, padded to the widest
// marker-plus-name-plus-profile among decorated entries; an entry with
// neither prints without one. Repo, when present, is rendered as its own
// "(repo)" parenthesis ahead of the count's "(n items)" — concept.md
// "Listing output": a name carried by two repositories in the rendered set
// is qualified with its repository, in the same trailing-parenthesis shape
// the count column already uses, ordered before the count when both are
// present on a row. This is CountedItems' alignment rule, folded into the
// one renderer so a block can carry markers, repo qualifiers, and counts
// together.
func RenderLines(entries []Entry, f *os.File) []string {
	width := 0
	for _, e := range entries {
		if e.Count > 0 || e.Repo != "" {
			if l := len(plainPrefix(e)); l > width {
				width = l
			}
		}
	}

	lines := make([]string, len(entries))
	for i, e := range entries {
		plain := plainPrefix(e)
		line := coloredPrefix(e, f)
		var parens []string
		if e.Repo != "" {
			parens = append(parens, fmt.Sprintf("(%s)", e.Repo))
		}
		if e.Count > 0 {
			parens = append(parens, fmt.Sprintf("(%s)", Plural(e.Count, "item")))
		}
		if len(parens) > 0 {
			paren := dimTone(strings.Join(parens, " "), f)
			pad := width - len(plain)
			if pad < 0 {
				pad = 0
			}
			line = line + strings.Repeat(" ", pad) + " " + paren
		}
		lines[i] = colorLine(e.Marker, line, f)
	}
	return lines
}

// plainPrefix is entry's marker/name/profile column, uncoloured — the width
// alignment above must measure this, never coloredPrefix, since an ANSI
// escape sequence would otherwise count toward the padding.
func plainPrefix(e Entry) string {
	p := fmt.Sprintf("%s %s", e.Marker, e.Name)
	if e.Profile != "" {
		p += fmt.Sprintf(" [%s]", e.Profile)
	}
	return p
}

// coloredPrefix is plainPrefix with the profile bracket in blue, per
// concept.md "Listing output": "the bracket is blue... making it match the
// row's marker would tie a piece of state to a colour that already means
// something else." The marker and name still pick up the row's marker tone
// afterward, from colorLine wrapping the whole line.
func coloredPrefix(e Entry, f *os.File) string {
	p := fmt.Sprintf("%s %s", e.Marker, e.Name)
	if e.Profile != "" {
		p += " " + blueTone(fmt.Sprintf("[%s]", e.Profile), f)
	}
	return p
}

// DetailSep separates a mutation report line's name from its trailing
// detail (a destination, or what occupies it) — a flat run of spaces reads
// as a column break without pretending to be alignment, since these lines
// are read one at a time rather than scanned as an aligned block. Exported so
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

// Blocked is one destination a mutation could not act on and why — the pair
// every caller-specific problem type (engine.Problem, selfheal.Problem)
// reduces to before handing it to BlockedSummary, so that function needs no
// knowledge of either package.
type Blocked struct {
	Dest   string
	Detail string
}

// BlockedSummary renders a namespace's blocked destinations onto the one
// line a mutation report gives that namespace, per concept.md "What enable
// reports": "the line carries the destination and the occupant only while
// the namespace has exactly one blocked destination. Past that it carries a
// count" — joining every reason onto one line rebuilds the run-on the cap
// exists to prevent. Dest is contracted for display; callers pass the
// absolute path, the same as Sub. Shared by enable's problemSummary and
// self-heal's disableReason so the two reports describe the same blocked
// destination identically (concept.md "Self-healing").
//
// state names what the collapsed count actually means — "occupied" only
// when every one of them holds a real file or directory, "blocked" for the
// in-repo link guard and anything else, per concept.md "What enable
// reports": "The collapsed count names what actually blocked it" — "occupied"
// is not a general synonym for "could not be linked".
func BlockedSummary(blocked []Blocked, state string) string {
	if len(blocked) == 1 {
		return manifest.ContractHome(blocked[0].Dest) + DetailSep + blocked[0].Detail
	}
	return fmt.Sprintf("%s %s", Plural(len(blocked), "destination"), state)
}

// BlockedTip prepends a "run `dots <namespace>`" clause for every namespace
// named, ahead of base, per concept.md "What enable reports": "the tip
// names `dots krita`" — the one command that expands a BlockedSummary count
// back into the per-entry detail a listing already knows how to render.
// Returns base unchanged when names is empty, so a caller can call this
// unconditionally.
func BlockedTip(names []string, base string) string {
	if len(names) == 0 {
		return base
	}
	cmds := make([]string, len(names))
	for i, name := range names {
		cmds[i] = fmt.Sprintf("`dots %s`", name)
	}
	return fmt.Sprintf("run %s to see them, %s", strings.Join(cmds, ", "), base)
}

// subIndent marks a Sub line as belonging to the Operation line above it —
// paths are not namespaces and never take a marker line of their own, per
// concept.md "What enable reports": "paths are not namespaces and do not
// take a marker line of their own."
const subIndent = "  "

// Sub formats an indented sub-line under an Operation line, reporting what
// happened to a path underneath it — a trashed occupant, most commonly.
// detail may be empty. path is always a destination, so this is where every
// Sub caller's destination gets ~-contracted for display, per concept.md
// "What enable reports": "Destinations print ~-contracted, everywhere dots
// prints one."
func Sub(marker, path, detail string) string {
	line := subIndent + fmt.Sprintf("%s %s", marker, manifest.ContractHome(path))
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
// a blank line, and an optional closing tip, followed by a single trailing
// newline — for reporting an actual list of things (as opposed to
// Confirm's single summary line under a prompt). tip may be empty.
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
		b.WriteString(tipTone("Tip: "+tip, os.Stderr))
	}
	b.WriteString("\n")
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
// (concept.md "Bootstrap"). RenderAligned's columns read off this glyph.
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

// WarningTone wraps msg in the warning tone used for the "!" marker, for
// message output that should read as the same kind of problem, per
// concept.md "Listing output". Coloured only when stderr is a terminal and
// NO_COLOR is unset.
func WarningTone(msg string) string {
	if !colorEnabled(os.Stderr) {
		return msg
	}
	return warningTone + msg + reset
}

// Tip wraps msg in the dim tone used for closing tips elsewhere (List's
// "Tip: " line), for a follow-up line printed under a fatal error.
func Tip(msg string) string {
	return dimTone(msg, os.Stderr)
}

// Prompt asks question, with options rendered inline in the conventional
// "(y/N)" form (the capitalized one signalling the default), and returns
// the raw response. context is optional leading material — a List or
// RenderAligned block, say — printed directly above question; pass "" when
// there is no such block. Prompt never inserts spacing of its own between
// context and question — context is responsible for its own trailing
// newline(s), same as every other producer here. Passing no options asks
// for free-text instead. Callers are responsible for validating the
// choice. It is an error to call this when Interactive() is false.
func Prompt(context, question string, options []string) (string, error) {
	if !Interactive() {
		return "", fmt.Errorf("cannot prompt: not an interactive session")
	}
	if len(options) > 0 {
		question = fmt.Sprintf("%s (%s)", question, strings.Join(options, "/"))
	}
	fmt.Fprintf(os.Stderr, "%s%s ", context, question)

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	return strings.TrimSpace(line), nil
}
