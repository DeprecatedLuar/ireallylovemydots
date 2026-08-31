package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DeprecatedLuar/dotz/internal/link"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/repo"
	"github.com/DeprecatedLuar/dotz/internal/state"
	"github.com/DeprecatedLuar/dotz/internal/trash"
)

const dirPerm = 0755

// ManifestEntries returns a namespace's tracked entries, reading them from
// the local filesystem when the namespace is already materialized, or from
// the repository's HEAD tree via git plumbing when it is not. This lets
// pre-flight run against a namespace's manifest before sparse checkout
// commits to materializing anything, so a namespace that fails pre-flight
// never touches the data directory at all.
func ManifestEntries(repoDir, namespaceDir, name string) (entries []manifest.Entry, materialized bool, err error) {
	if _, statErr := os.Stat(namespaceDir); statErr == nil {
		m, readErr := manifest.Read(namespaceDir)
		if readErr != nil {
			return nil, true, readErr
		}
		return m.Entries, true, nil
	} else if !os.IsNotExist(statErr) {
		return nil, false, fmt.Errorf("stat namespace directory %s: %w", namespaceDir, statErr)
	}

	cmd := exec.Command("git", "-C", repoDir, "show", fmt.Sprintf("HEAD:%s/.dots", name))
	out, cmdErr := cmd.Output()
	if cmdErr != nil {
		return nil, false, fmt.Errorf("namespace %q not found in repository", name)
	}
	m, decodeErr := manifest.Decode(out)
	if decodeErr != nil {
		return nil, false, fmt.Errorf("parse manifest for namespace %q: %w", name, decodeErr)
	}
	return m.Entries, false, nil
}

// Materialize brings a namespace's folder into the working tree by adding
// it to the repository's sparse-checkout cone, when it is not already
// present on disk. A namespace created directly (namespace add, namespace
// <ns> add) is already there, and this is a no-op — the ordinary case for
// a namespace that has never been synced anywhere else, and for a
// namespace being enabled a second time after a disable. Exported for
// `install`, which is exactly this operation on its own, per concept.md
// "Install and uninstall".
func Materialize(repoDir, namespaceDir, name string) error {
	if _, err := os.Stat(namespaceDir); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat namespace directory %s: %w", namespaceDir, err)
	}

	if err := repo.Add(repoDir, name); err != nil {
		return fmt.Errorf("sparse-checkout namespace %q: %w", name, err)
	}
	if _, err := os.Stat(namespaceDir); err != nil {
		return fmt.Errorf("namespace %q was not materialized by sparse checkout", name)
	}
	return nil
}

type trashedEntry struct {
	dest   string
	name   string
	detail string
}

// TrashedDestination records one destination Enable found occupied and
// trashed before linking, so a caller can report it as a Sub line under the
// namespace's Operation line, per concept.md "What enable reports": "what
// was trashed is reported as an indented sub-line beneath its namespace."
type TrashedDestination struct {
	Dest   string
	Detail string
}

// Enable materializes the namespace via sparse checkout if needed, disables
// every namespace named by a Collision problem, trashes every destination
// named by a confirmed Occupied problem, then links every entry, parent
// before child by path depth, per concept.md "Enable". It returns every
// destination it trashed, in link order, for the caller to report.
//
// State is written only once every link has succeeded — never before,
// unlike the narrative order in concept.md — so that a failure partway
// through leaves neither a link nor a state entry behind: rolling back a
// state entry that was already persisted is one more thing that could fail,
// where never persisting it in the first place cannot.
func Enable(key state.Key, repoDir, namespaceDir, name string, entries []manifest.Entry, s state.State, problems []Problem) ([]TrashedDestination, error) {
	for _, p := range problems {
		if p.Kind == Collision {
			if err := disableConflicting(*p.Conflicting, s); err != nil {
				return nil, err
			}
		}
	}

	if err := Materialize(repoDir, namespaceDir, name); err != nil {
		return nil, err
	}

	occupiedDetail := map[string]string{}
	for _, p := range problems {
		if p.Kind == Occupied {
			occupiedDetail[p.Entry.Dest] = OccupancyDetail(p.Message)
		}
	}

	sorted := sortByDepth(entries)

	var created []string
	var trashed []trashedEntry
	rollback := func() {
		for i := len(created) - 1; i >= 0; i-- {
			link.Remove(created[i])
		}
		for i := len(trashed) - 1; i >= 0; i-- {
			trash.Restore(trashed[i].name, trashed[i].dest)
		}
	}

	for _, e := range sorted {
		if detail, ok := occupiedDetail[e.Dest]; ok {
			trashedName, err := trash.Move(e.Dest)
			if err != nil {
				rollback()
				return nil, fmt.Errorf("trash occupied destination %s: %w", e.Dest, err)
			}
			trashed = append(trashed, trashedEntry{dest: e.Dest, name: trashedName, detail: detail})
		}
		if err := os.MkdirAll(filepath.Dir(e.Dest), dirPerm); err != nil {
			rollback()
			return nil, fmt.Errorf("create parent directory for %s: %w", e.Dest, err)
		}
		payload := filepath.Join(namespaceDir, e.Name)
		if err := link.Create(e.Dest, payload); err != nil {
			rollback()
			return nil, err
		}
		created = append(created, e.Dest)
	}

	dests := make([]string, len(sorted))
	for i, e := range sorted {
		dests[i] = e.Dest
	}
	s.Entries[key] = state.Entry{Enabled: true, LinkedDests: dests}
	if err := state.Write(s); err != nil {
		rollback()
		return nil, err
	}

	result := make([]TrashedDestination, len(trashed))
	for i, t := range trashed {
		result[i] = TrashedDestination{Dest: t.dest, Detail: t.detail}
	}
	return result, nil
}

// OccupancyDetail pulls the parenthesised detail (e.g. "real directory, 340
// files") out of an Occupied problem's message, so Enable's trash report —
// and any command's skip-report line, e.g. problemSummary in
// internal/commands/enable.go — can reuse the same wording pre-flight
// already computed rather than re-deriving it.
func OccupancyDetail(msg string) string {
	open := strings.IndexByte(msg, '(')
	close := strings.IndexByte(msg, ')')
	if open == -1 || close == -1 || close < open {
		return ""
	}
	return msg[open+1 : close]
}

// disableConflicting disables an entire namespace found to conflict during
// pre-flight, per concept.md "Conflicts": the whole namespace is disabled,
// never the overlapping subset.
func disableConflicting(key state.Key, s state.State) error {
	return Disable(key, s)
}

// sortByDepth returns entries sorted parent before child by destination
// path depth, per concept.md "Enable": without a fixed order, a namespace
// holding both a directory entry and an entry beneath it produces a
// different outcome depending on which happens to link first.
func sortByDepth(entries []manifest.Entry) []manifest.Entry {
	sorted := append([]manifest.Entry{}, entries...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return depth(sorted[i].Dest) < depth(sorted[j].Dest)
	})
	return sorted
}

func depth(p string) int {
	return strings.Count(filepath.Clean(p), string(filepath.Separator))
}
