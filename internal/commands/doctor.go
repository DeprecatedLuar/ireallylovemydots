package commands

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/namespace"
	"github.com/DeprecatedLuar/dotz/internal/paths"
	"github.com/DeprecatedLuar/dotz/internal/repo"
	"github.com/DeprecatedLuar/dotz/internal/selfheal"
	"github.com/DeprecatedLuar/dotz/internal/state"
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
	if err := renderReadOnlyFindings(); err != nil {
		return err
	}
	if err := renderWhitelistFindings(); err != nil {
		return err
	}
	return nil
}

// renderReadOnlyFindings reports every repository this machine has recorded
// as read-only, per Phase 13: a fact worth surfacing on every invocation,
// same as every other self-heal finding, even though it isn't self-heal's
// own drift to report — sync will fetch-only for these until push access is
// restored.
func renderReadOnlyFindings() error {
	reg, err := manifest.ReadRegistry()
	if err != nil {
		return err
	}
	access, err := state.ReadAccess()
	if err != nil {
		return err
	}

	var names []string
	for _, r := range reg.Repos {
		if access.IsReadOnly(r.Name) {
			names = append(names, r.Name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)

	entries := make([]ui.Entry, len(names))
	for i, name := range names {
		entries[i] = ui.Entry{Marker: ui.MarkerProblem, Name: name + ui.DetailSep + "read-only on this machine — sync will fetch only"}
	}
	renderListing(entries)
	return nil
}

// renderWhitelistFindings reports two kinds of drift between a repository's
// Phase 15 namespaces whitelist and reality, same as renderReadOnlyFindings:
// a namespace materialized on this machine that the whitelist no longer
// names — not an error, since manifest.Repo.Allows keeps an installed
// namespace visible until it is uninstalled — and a whitelist entry naming a
// namespace the repository's catalogue does not contain at all.
func renderWhitelistFindings() error {
	reg, err := manifest.ReadRegistry()
	if err != nil {
		return err
	}
	dataDir, err := paths.Data()
	if err != nil {
		return err
	}

	var entries []ui.Entry
	for _, r := range reg.Repos {
		if len(r.Namespaces) == 0 {
			continue
		}
		repoDir := filepath.Join(dataDir, r.Name)

		installed, err := namespace.LocalNames(repoDir)
		if err != nil {
			return err
		}
		for _, n := range installed {
			if !r.Allows(n, false) {
				entries = append(entries, ui.Entry{Marker: ui.MarkerProblem, Name: n + ui.DetailSep + fmt.Sprintf("installed but not in repository %q's namespaces whitelist; stays listed until uninstalled", r.Name)})
			}
		}

		catalogue, err := repo.Namespaces(repoDir)
		if err != nil {
			return err
		}
		for _, n := range r.Namespaces {
			if !slices.Contains(catalogue, n) {
				entries = append(entries, ui.Entry{Marker: ui.MarkerProblem, Name: n + ui.DetailSep + fmt.Sprintf("named in repository %q's namespaces whitelist but not found in it", r.Name)})
			}
		}
	}
	if len(entries) == 0 {
		return nil
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	renderListing(entries)
	return nil
}

// renderFindings prints findings through the shared listing renderer, so a
// finding's marker and colour match the "!" a listing would have shown for
// the same namespace — shared by doctor's machine-wide scope and dots
// <ns>'s findings block (renderNamespaceEntries), narrowed by
// selfheal.Findings.For. Subjects are padded to the widest one in the block
// so the detail text lines up in a column, matching concept.md "Doctor"'s
// own example — DetailSep's flat gap is for a single report line, not a
// block of them.
func renderFindings(findings []selfheal.Finding) {
	if len(findings) == 0 {
		return
	}
	width := 0
	for _, f := range findings {
		if l := len(f.Subject); l > width {
			width = l
		}
	}
	entries := make([]ui.Entry, len(findings))
	for i, f := range findings {
		pad := strings.Repeat(" ", width-len(f.Subject))
		entries[i] = ui.Entry{Marker: ui.MarkerProblem, Name: f.Subject + pad + ui.DetailSep + findingDetail(f)}
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
