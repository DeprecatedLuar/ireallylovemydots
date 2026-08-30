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

// materialize sparse-checks-out a namespace's folder from HEAD when it is
// not already present on disk. A namespace created directly (namespace add,
// namespace <ns> add) is already there, and this is a no-op — the ordinary
// case for a namespace that has never been synced anywhere else, and for a
// namespace being enabled a second time after a disable.
func materialize(repoDir, namespaceDir, name string) error {
	if _, err := os.Stat(namespaceDir); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat namespace directory %s: %w", namespaceDir, err)
	}

	cmd := exec.Command("git", "-C", repoDir, "checkout", "HEAD", "--", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("checkout namespace %q: %s", name, strings.TrimSpace(string(out)))
	}
	if _, err := os.Stat(namespaceDir); err != nil {
		return fmt.Errorf("namespace %q was not materialized by checkout", name)
	}
	return nil
}

type trashedEntry struct {
	dest string
	name string
}

// Enable materializes the namespace via sparse checkout if needed, disables
// every namespace named by a Collision problem, trashes every destination
// named by a confirmed Occupied problem, then links every entry, parent
// before child by path depth, per concept.md "Enable".
//
// State is written only once every link has succeeded — never before,
// unlike the narrative order in concept.md — so that a failure partway
// through leaves neither a link nor a state entry behind: rolling back a
// state entry that was already persisted is one more thing that could fail,
// where never persisting it in the first place cannot.
func Enable(key state.Key, repoDir, namespaceDir, name string, entries []manifest.Entry, s state.State, problems []Problem) error {
	for _, p := range problems {
		if p.Kind == Collision {
			if err := disableConflicting(*p.Conflicting, s); err != nil {
				return err
			}
		}
	}

	if err := materialize(repoDir, namespaceDir, name); err != nil {
		return err
	}

	occupied := map[string]bool{}
	for _, p := range problems {
		if p.Kind == Occupied {
			occupied[p.Entry.Dest] = true
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
		if occupied[e.Dest] {
			trashedName, err := trash.Move(e.Dest)
			if err != nil {
				rollback()
				return fmt.Errorf("trash occupied destination %s: %w", e.Dest, err)
			}
			trashed = append(trashed, trashedEntry{dest: e.Dest, name: trashedName})
		}
		if err := os.MkdirAll(filepath.Dir(e.Dest), dirPerm); err != nil {
			rollback()
			return fmt.Errorf("create parent directory for %s: %w", e.Dest, err)
		}
		payload := filepath.Join(namespaceDir, e.Name)
		if err := link.Create(e.Dest, payload); err != nil {
			rollback()
			return err
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
		return err
	}
	return nil
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
