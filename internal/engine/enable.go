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
	"github.com/DeprecatedLuar/dotz/internal/profile"
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

// absorbedEntry records one destination Enable cleared without trashing —
// a symlink or an empty directory, neither of which held data — so it can
// be recreated if a later entry in the same batch fails.
type absorbedEntry struct {
	dest       string
	wasSymlink bool
	target     string // only meaningful when wasSymlink
}

// clearAbsorbable removes dest when pre-flight's occupancy test would have
// absorbed it silently — a symlink, dangling or not, or an empty directory —
// and reports what it removed so Enable can recreate it on rollback. Nothing
// else can be there: a real file or non-empty directory was already flagged
// Occupied and trashed above pre-flight ran, per concept.md "Occupied
// destinations". Returns nil, nil when dest does not exist — the ordinary
// case, nothing to clear.
func clearAbsorbable(dest string) (*absorbedEntry, error) {
	info, err := os.Lstat(dest)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lstat %s: %w", dest, err)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		target, readErr := link.Read(dest)
		if readErr != nil {
			return nil, readErr
		}
		if err := link.Remove(dest); err != nil {
			return nil, err
		}
		return &absorbedEntry{dest: dest, wasSymlink: true, target: target}, nil
	}

	if info.IsDir() {
		if err := os.Remove(dest); err != nil {
			return nil, fmt.Errorf("remove empty directory %s: %w", dest, err)
		}
		return &absorbedEntry{dest: dest}, nil
	}

	return nil, nil
}

// ReplacedDestination records one destination Enable found something at and
// removed before linking — either trashed (it held data) or simply cleared
// (a symlink or empty directory, holding none) — so a caller can report it
// as a Sub line under the namespace's Operation line, per concept.md "What
// enable reports": "what was trashed is reported as an indented sub-line
// beneath its namespace," and concept.md "Occupied destinations": an
// absorbed symlink "is still reported as a sub-line under the entry it
// replaced."
type ReplacedDestination struct {
	Dest   string
	Detail string
}

// Enable materializes the namespace via sparse checkout if needed, disables
// every namespace named by a Collision problem, trashes every destination
// named by a confirmed Occupied problem, clears every destination pre-flight
// found absorbable (a symlink or an empty directory — concept.md "Occupied
// destinations": neither holds data), then links every entry, parent before
// child by path depth, per concept.md "Enable". It returns every destination
// it trashed or cleared, in link order, for the caller to report.
//
// State is written only once every link has succeeded — never before,
// unlike the narrative order in concept.md — so that a failure partway
// through leaves neither a link nor a state entry behind: rolling back a
// state entry that was already persisted is one more thing that could fail,
// where never persisting it in the first place cannot.
func Enable(key state.Key, repoDir, namespaceDir, name string, entries []manifest.Entry, s state.State, problems []Problem) ([]ReplacedDestination, error) {
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
	var absorbed []absorbedEntry
	var replaced []ReplacedDestination
	rollback := func() {
		for i := len(created) - 1; i >= 0; i-- {
			link.Remove(created[i])
		}
		for i := len(absorbed) - 1; i >= 0; i-- {
			a := absorbed[i]
			if a.wasSymlink {
				link.Create(a.dest, a.target)
			} else {
				os.Mkdir(a.dest, dirPerm)
			}
		}
		for i := len(trashed) - 1; i >= 0; i-- {
			trash.Restore(trashed[i].name, trashed[i].dest)
		}
	}

	for _, e := range sorted {
		if !e.HasDestination() {
			// An empty destination or manifest.DestNone names nothing to
			// link — there is no symlink to create for it.
			continue
		}
		if detail, ok := occupiedDetail[e.Dest]; ok {
			trashedName, err := trash.Move(e.Dest)
			if err != nil {
				rollback()
				return nil, fmt.Errorf("trash occupied destination %s: %w", e.Dest, err)
			}
			trashed = append(trashed, trashedEntry{dest: e.Dest, name: trashedName, detail: detail})
			replaced = append(replaced, ReplacedDestination{Dest: e.Dest, Detail: detail + " -> trash"})
		} else if cleared, err := clearAbsorbable(e.Dest); err != nil {
			rollback()
			return nil, fmt.Errorf("clear %s: %w", e.Dest, err)
		} else if cleared != nil {
			absorbed = append(absorbed, *cleared)
			// concept.md "Occupied destinations": an absorbed symlink or
			// empty directory held nothing of the user's, so it's absorbed
			// as silently as an absent destination — only real files/dirs
			// (handled above via the trash path) are worth reporting.
		}
		if err := os.MkdirAll(filepath.Dir(e.Dest), dirPerm); err != nil {
			rollback()
			return nil, fmt.Errorf("create parent directory for %s: %w", e.Dest, err)
		}
		payload, err := profile.Source(namespaceDir, e.Name, s.Entries[key].ActiveProfile)
		if err != nil {
			rollback()
			return nil, err
		}
		if err := link.Create(e.Dest, payload); err != nil {
			rollback()
			return nil, err
		}
		created = append(created, e.Dest)
	}

	var dests []string
	for _, e := range sorted {
		if e.HasDestination() {
			dests = append(dests, e.Dest)
		}
	}
	// The active profile survives enable and disable alike: it says which
	// version of an entry belongs at a destination, not whether anything is
	// linked, so re-enabling a namespace must put back what was there before.
	s.Entries[key] = state.Entry{Enabled: true, ActiveProfile: s.Entries[key].ActiveProfile, LinkedDests: dests}
	if err := state.Write(s); err != nil {
		rollback()
		return nil, err
	}

	return replaced, nil
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
