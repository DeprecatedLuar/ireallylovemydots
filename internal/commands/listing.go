package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"

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
// materialized namespaces marked "-", and namespaces that exist in a
// repository's catalogue but are not materialized here, marked "=". Drift
// and conflict markers ("!", "?") are phase 10's job. The catalogue walk
// (repo.Namespaces) is skipped for a repository when "=" is excluded by
// opts.States, and each namespace's manifest is read only when opts.Counts
// is set — a bare listing pays for neither.
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
			row := namespaceRow(s, r.Name, n)
			if !opts.wantState(row.Marker) {
				continue
			}
			if opts.Counts {
				m, err := manifest.Read(filepath.Join(repoDir, n))
				if err != nil {
					return nil, err
				}
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
			if !localSet[n] {
				rows = append(rows, ui.Entry{Marker: ui.MarkerAbsent, Name: n})
			}
		}
	}
	sortNamespaceRows(rows)
	return rows, nil
}

// namespaceRow is the one place a namespace's listing marker is decided:
// enabled materialized namespaces are "+", every other materialized
// namespace is "-". Phase 10 adds "!" here for drift and conflicts.
func namespaceRow(s state.State, repoName, nsName string) ui.Entry {
	marker := ui.MarkerMaterialized
	if s.Entries[state.Key{Repo: repoName, Namespace: nsName}].Enabled {
		marker = ui.MarkerEnabled
	}
	return ui.Entry{Marker: marker, Name: nsName}
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

// entryListing enumerates one namespace's tracked entries. Per-entry markers
// beyond "-" — "+" for a linked entry, "!"/"?" for drift and untracked
// payloads — arrive with self-healing in phase 10.
func entryListing(entries []manifest.Entry) []ui.Entry {
	rows := make([]ui.Entry, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, ui.Entry{Marker: ui.MarkerMaterialized, Name: e.Name})
	}
	return rows
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
		iRank := namespaceRowRank(rows[i].Marker)
		jRank := namespaceRowRank(rows[j].Marker)
		if iRank != jRank {
			return iRank < jRank
		}
		return rows[i].Name < rows[j].Name
	})
}

func namespaceRowRank(marker string) int {
	switch marker {
	case ui.MarkerProblem:
		return 0
	case ui.MarkerEnabled:
		return 1
	case ui.MarkerMaterialized:
		return 2
	case ui.MarkerAbsent:
		return 3
	default:
		return 4
	}
}
