package commands

import (
	"fmt"

	"github.com/DeprecatedLuar/dotz/internal/selfheal"
	"github.com/DeprecatedLuar/dotz/internal/ui"
)

// HandleDoctor implements `dots doctor`, per concept.md "Doctor": every
// finding self-heal has on record, one line per finding — marker, subject,
// what is wrong, and, where there is one, the command that resolves it.
// It only reports: exit status stays zero even with findings ("drift is a
// state, not a failure of the invocation that reported it"), and nothing
// here mutates anything.
func HandleDoctor(args []string, findings selfheal.Findings) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: doctor")
	}
	renderFindings(findings.All())
	return nil
}

// renderFindings prints findings through the shared listing renderer, so a
// finding's marker and colour match the "!" a listing would have shown for
// the same namespace — shared by doctor's machine-wide scope and dots
// <ns>'s findings block (renderNamespaceEntries), narrowed by
// selfheal.Findings.For.
func renderFindings(findings []selfheal.Finding) {
	if len(findings) == 0 {
		return
	}
	entries := make([]ui.Entry, len(findings))
	for i, f := range findings {
		entries[i] = ui.Entry{Marker: ui.MarkerProblem, Name: f.Subject + ui.DetailSep + findingDetail(f)}
	}
	renderListing(entries)
}

// findingDetail appends a finding's fix, when it has one, in the same
// "<detail>, run: <command>" shape concept.md "Doctor"'s examples use.
func findingDetail(f selfheal.Finding) string {
	if f.Fix == "" {
		return f.Detail
	}
	return fmt.Sprintf("%s, run: %s", f.Detail, f.Fix)
}
