package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/DeprecatedLuar/dotz/internal/git"
	"github.com/DeprecatedLuar/dotz/internal/link"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/namespace"
	"github.com/DeprecatedLuar/dotz/internal/paths"
	"github.com/DeprecatedLuar/dotz/internal/repo"
	"github.com/DeprecatedLuar/dotz/internal/state"
	"github.com/DeprecatedLuar/dotz/internal/ui"
)

// This file owns every row dots's listings produce. concept.md "Listing
// output": every listing is the same renderer; a caller may narrow its
// scope to one repository or to the states relevant to what it's about to
// do, but it never builds ui.Entry values of its own.

// listOptions narrows and decorates a namespace listing.
type listOptions struct {
	Repo   string   // "" = every registered repository
	States []string // nil = every state; else only rows whose marker is in this set
	Counts bool     // read each namespace's manifest and set Entry.Count to its entry count
}

// wantState reports whether marker passes opts' state filter.
func (o listOptions) wantState(marker string) bool {
	if len(o.States) == 0 {
		return true
	}
	return slices.Contains(o.States, marker)
}

// namespaceListing enumerates namespaces across repos, per concept.md
// "Listing output": enabled materialized namespaces marked "+", disabled
// materialized namespaces marked "-", namespaces that exist in a
// repository's catalogue but are not materialized here marked "=", and any
// materialized namespace holding an orphaned, invalid, or untracked
// entry underneath it marked "!" — concept.md "Manual edits": "a `!`
// namespace always has at least one `!` or `?` entry underneath it". The
// catalogue walk (repo.Namespaces) is skipped for a repository when "="
// is excluded by opts.States. Every materialized namespace's manifest is
// read regardless of opts.Counts now, since deciding "+"/"-" vs "!" needs
// it; self-heal (run once ahead of every dispatch, in cmd/dotz/main.go) has
// already corrected whatever it could by the time this runs, so what is
// left to find here is exactly what self-heal would not touch.
func namespaceListing(repos []manifest.Repo, opts listOptions) ([]ui.Entry, error) {
	dataDir, err := paths.Data()
	if err != nil {
		return nil, err
	}
	s, err := state.Read()
	if err != nil {
		return nil, err
	}

	var rows []ui.Entry
	for _, r := range repos {
		if opts.Repo != "" && r.Name != opts.Repo {
			continue
		}
		repoDir := filepath.Join(dataDir, r.Name)

		local, err := namespace.LocalNames(repoDir)
		if err != nil {
			return nil, err
		}
		localSet := make(map[string]bool, len(local))
		for _, n := range local {
			localSet[n] = true
			nsDir := filepath.Join(repoDir, n)
			m, err := manifest.Read(nsDir)
			if err != nil {
				return nil, err
			}
			// A namespace declaring itself out of scope is invisible here,
			// per concept.md "Namespace" — the same rule as a folder with
			// no .dots at all.
			if m.Ignore {
				continue
			}
			row, err := namespaceRow(s, r.Name, n, nsDir, m.Entries)
			if err != nil {
				return nil, err
			}
			if !opts.wantState(row.Marker) {
				continue
			}
			if opts.Counts {
				row.Count = len(m.Entries)
			}
			rows = append(rows, row)
		}

		if !opts.wantState(ui.MarkerAbsent) {
			continue
		}
		catalogue, err := repo.Namespaces(repoDir)
		if err != nil {
			return nil, err
		}
		for _, n := range catalogue {
			if localSet[n] {
				continue
			}
			ignored, err := catalogueNamespaceIgnored(repoDir, n)
			if err != nil {
				return nil, err
			}
			if ignored {
				continue
			}
			rows = append(rows, ui.Entry{Marker: ui.MarkerAbsent, Name: n})
		}
	}
	sortNamespaceRows(rows)
	return rows, nil
}

// namespaceRow is the one place a namespace's listing marker is decided:
// enabled materialized namespaces are "+", every other materialized
// namespace is "-", unless namespaceProblems finds a "!" or "?" entry
// underneath it, which promotes the namespace itself to "!".
func namespaceRow(s state.State, repoName, nsName, namespaceDir string, entries []manifest.Entry) (ui.Entry, error) {
	stateEntry := s.Entries[state.Key{Repo: repoName, Namespace: nsName}]
	marker := ui.MarkerMaterialized
	if stateEntry.Enabled {
		marker = ui.MarkerEnabled
	}

	rows, _, _, err := namespaceProblems(namespaceDir, entries, stateEntry.Enabled)
	if err != nil {
		return ui.Entry{}, err
	}
	for _, row := range rows {
		if row.Marker == ui.MarkerProblem || row.Marker == ui.MarkerUntracked {
			marker = ui.MarkerProblem
			break
		}
	}
	return ui.Entry{Marker: marker, Name: nsName}, nil
}

// catalogueNamespaceIgnored reports whether a namespace not materialized
// locally is nonetheless ignored, per its committed manifest. repo.Namespaces
// only sees the git tree's shape (repo.RootEntries is tree-only), not blob
// content, so the manifest itself has to be read from HEAD via git.ShowFile
// — the same primitive self-heal's manifest recovery uses.
func catalogueNamespaceIgnored(repoDir, name string) (bool, error) {
	data, found, err := git.ShowFile(repoDir, "HEAD", filepath.Join(name, ".dots"))
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	m, err := manifest.Decode(data)
	if err != nil {
		return false, nil
	}
	return m.Ignore, nil
}

// repoListing enumerates registered repositories, marking one whose clone
// is missing from the data directory "!" rather than silently deregistering
// it — concept.md "Repository manifest": absence is ambiguous, and a
// mis-set XDG_DATA_HOME must not be able to erase the registry.
func repoListing(repos []manifest.Repo) ([]ui.Entry, error) {
	dataDir, err := paths.Data()
	if err != nil {
		return nil, err
	}
	rows := make([]ui.Entry, 0, len(repos))
	for _, r := range repos {
		marker := ui.MarkerMaterialized
		if _, err := os.Stat(filepath.Join(dataDir, r.Name)); os.IsNotExist(err) {
			marker = ui.MarkerProblem
		}
		rows = append(rows, ui.Entry{Marker: marker, Name: r.Name})
	}
	return rows, nil
}

// entryListing enumerates one namespace's tracked entries plus any
// untracked payloads found beside them, and proposes a rename when the scan
// finds exactly one orphan and one untracked file together — concept.md
// "Manual edits": "An orphan and an untracked file in the same namespace is
// almost certainly a rename. Listing may suggest it — the one place a
// listing prints more than a marker and a name." The suggestion is a plain
// string for the caller to print as a tip; entryListing never prints.
func entryListing(namespaceDir string, entries []manifest.Entry, enabled bool) (rows []ui.Entry, suggestion string, err error) {
	rows, orphans, untracked, err := namespaceProblems(namespaceDir, entries, enabled)
	if err != nil {
		return nil, "", err
	}
	sortEntryRows(rows)
	if len(orphans) == 1 && len(untracked) == 1 {
		suggestion = fmt.Sprintf("%s looks renamed to %s, track it under its new name, then remove the old entry", orphans[0], untracked[0])
	}
	return rows, suggestion, nil
}

// namespaceProblems classifies namespace's tracked entries and finds any
// untracked payload beside them, per concept.md "Manual edits", from one
// shared directory walk (namespace.Inspect) also used by self-heal's repair
// warning. orphans and untracked name the entries/payloads driving each half
// of the rename suggestion; a caller that only needs the namespace-level "!"
// rollup (namespaceRow) ignores them.
func namespaceProblems(namespaceDir string, entries []manifest.Entry, enabled bool) (rows []ui.Entry, orphans, untracked []string, err error) {
	report, err := namespace.Inspect(namespaceDir, entries)
	if err != nil {
		return nil, nil, nil, err
	}

	invalid := toSet(report.Invalid)
	orphaned := toSet(report.Orphans)

	rows = make([]ui.Entry, 0, len(entries)+len(report.Untracked))
	for _, e := range entries {
		rows = append(rows, classifyEntry(e, namespaceDir, enabled, invalid, orphaned))
	}
	for _, name := range report.Untracked {
		rows = append(rows, ui.Entry{Marker: ui.MarkerUntracked, Name: name})
	}
	return rows, report.Orphans, report.Untracked, nil
}

// classifyEntry decides one tracked entry's listing marker, per concept.md
// "Manual edits": "?" for an entry with an empty destination — invalid, not
// pending — "!" for an orphan (its payload is gone from the namespace), "+"
// for a correctly linked entry in an enabled namespace, "-" otherwise.
// invalid and orphaned are namespaceProblems' sets from namespace.Inspect's
// facts; this function only decides the marker, and additionally runs the
// link-classification check Inspect deliberately leaves out (it needs
// enabled, which is a listing/self-heal concern, not a directory fact) as a
// safety net for drift within the same invocation, between self-heal's pass
// and this one — self-heal itself now disables a namespace outright rather
// than leaving a blocked destination "!" here, per concept.md
// "Self-healing".
func classifyEntry(e manifest.Entry, namespaceDir string, enabled bool, invalid, orphaned map[string]bool) ui.Entry {
	if invalid[e.Name] {
		return ui.Entry{Marker: ui.MarkerUntracked, Name: e.Name}
	}
	if orphaned[e.Name] {
		return ui.Entry{Marker: ui.MarkerProblem, Name: e.Name}
	}
	if e.Dest == manifest.DestNone {
		// Deliberately unlinked — nothing to classify against the
		// filesystem, so it reads the same whether the namespace is
		// enabled or not.
		return ui.Entry{Marker: ui.MarkerMaterialized, Name: e.Name}
	}

	if !enabled {
		return ui.Entry{Marker: ui.MarkerMaterialized, Name: e.Name}
	}
	payload := filepath.Join(namespaceDir, e.Name)
	if st, classifyErr := link.Classify(e.Dest, payload); classifyErr != nil || st != link.CorrectSymlink {
		return ui.Entry{Marker: ui.MarkerProblem, Name: e.Name}
	}
	return ui.Entry{Marker: ui.MarkerEnabled, Name: e.Name}
}

// toSet turns a name slice into a membership set, for classifyEntry's O(1)
// lookups against namespace.Inspect's Invalid/Orphans facts.
func toSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}

// renderListing prints rows through the shared renderer — the single sink
// every listing command prints through.
func renderListing(rows []ui.Entry) {
	fmt.Print(ui.Render(rows))
}

// sortNamespaceRows groups namespaces by listing state, then sorts names
// within each state. Problems lead, followed by enabled and disabled
// materialized namespaces, with unavailable namespaces last.
func sortNamespaceRows(rows []ui.Entry) {
	sort.SliceStable(rows, func(i, j int) bool {
		iRank := markerRank(rows[i].Marker)
		jRank := markerRank(rows[j].Marker)
		if iRank != jRank {
			return iRank < jRank
		}
		return rows[i].Name < rows[j].Name
	})
}

// sortEntryRows applies the same state-then-name grouping to one
// namespace's entry rows, so a "!" or "?" entry leads a per-namespace
// listing the same way a "!" namespace leads the top-level one.
func sortEntryRows(rows []ui.Entry) {
	sort.SliceStable(rows, func(i, j int) bool {
		iRank := markerRank(rows[i].Marker)
		jRank := markerRank(rows[j].Marker)
		if iRank != jRank {
			return iRank < jRank
		}
		return rows[i].Name < rows[j].Name
	})
}

func markerRank(marker string) int {
	switch marker {
	case ui.MarkerProblem:
		return 0
	case ui.MarkerUntracked:
		return 1
	case ui.MarkerEnabled:
		return 2
	case ui.MarkerMaterialized:
		return 3
	case ui.MarkerAbsent:
		return 4
	default:
		return 5
	}
}
