package commands

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/engine"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/namespace"
	"github.com/DeprecatedLuar/dotz/internal/paths"
	"github.com/DeprecatedLuar/dotz/internal/repo"
	"github.com/DeprecatedLuar/dotz/internal/state"
	"github.com/DeprecatedLuar/dotz/internal/trash"
	"github.com/DeprecatedLuar/dotz/internal/ui"
)

// rmEntry implements `namespace <ns> rm <path>`, per concept.md "Removal":
// the entry leaves the manifest, the symlink comes down, and the real file
// returns to its destination — regardless of whether the namespace is
// currently enabled.
func rmEntry(name, path string, flags shared.Flags) error {
	loc, err := resolveNamespace(name, flags)
	if err != nil {
		return err
	}
	dest, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve absolute path for %s: %w", path, err)
	}

	m, err := manifest.Read(loc.Dir)
	if err != nil {
		return err
	}
	entry, ok := entryByDest(m, dest)
	if !ok {
		return fmt.Errorf("%s is not tracked in namespace %q", dest, name)
	}

	return restoreEntries(loc, name, []manifest.Entry{entry}, flags)
}

// rmNamespace implements `namespace rm <ns>`: every entry is restored to its
// destination, then the namespace folder is removed. Enabled state does not
// change this — a disabled namespace has no symlinks, so restoring writes
// files to destinations that are currently empty.
func rmNamespace(name string, flags shared.Flags) error {
	loc, err := resolveNamespace(name, flags)
	if err != nil {
		return err
	}
	if err := rmNamespaceAt(loc, name, flags); err != nil {
		return err
	}
	return namespace.Delete(loc.Dir)
}

// rmRepo implements `repo rm <repo>`: the same restore path as
// `namespace rm`, for every namespace in the repository, then the clone and
// its registry entry are removed.
func rmRepo(name string, flags shared.Flags) error {
	reg, err := manifest.ReadRegistry()
	if err != nil {
		return err
	}
	r, err := repo.Resolve(reg.Repos, name)
	if err != nil {
		return err
	}
	dataDir, err := paths.Data()
	if err != nil {
		return err
	}
	repoDir := filepath.Join(dataDir, r.Name)

	names, err := namespace.LocalNames(repoDir)
	if err != nil {
		return err
	}
	for _, nsName := range names {
		loc := namespace.Located{Repo: r, Dir: filepath.Join(repoDir, nsName)}
		if err := rmNamespaceAt(loc, nsName, flags); err != nil {
			return err
		}
	}

	if _, err := trash.Move(repoDir); err != nil {
		return fmt.Errorf("trash repository %s: %w", repoDir, err)
	}

	remaining := make([]manifest.Repo, 0, len(reg.Repos))
	for _, existing := range reg.Repos {
		if existing.Name != r.Name {
			remaining = append(remaining, existing)
		}
	}
	reg.Repos = remaining
	return manifest.WriteRegistry(reg)
}

// rmNamespaceAt restores every entry of the namespace at loc and clears its
// machine state, without touching the namespace folder itself — the shared
// step between `namespace rm <ns>` (which then deletes the folder) and
// `repo rm <repo>` (which deletes the whole clone afterwards).
func rmNamespaceAt(loc namespace.Located, nsName string, flags shared.Flags) error {
	m, err := manifest.Read(loc.Dir)
	if err != nil {
		return err
	}
	if len(m.Entries) > 0 {
		if err := restoreEntries(loc, nsName, m.Entries, flags); err != nil {
			return err
		}
	}
	return clearState(loc.Repo.Name, nsName)
}

// clearState drops a namespace's machine-state entry entirely. Used once the
// namespace itself is going away, unlike a partial `namespace <ns> rm <path>`
// which only narrows the recorded linked destinations.
func clearState(repoName, nsName string) error {
	s, err := state.Read()
	if err != nil {
		return err
	}
	key := state.Key{Repo: repoName, Namespace: nsName}
	if _, ok := s.Entries[key]; !ok {
		return nil
	}
	delete(s.Entries, key)
	return state.Write(s)
}

// restoreEntries runs pre-flight for entries within loc, resolves any
// occupied-destination conflicts through the same single-confirmation shape
// as enable (concept.md "Occupied destinations on removal"), then restores —
// or, under --purge, trashes — every entry via engine.Restore.
func restoreEntries(loc namespace.Located, nsName string, entries []manifest.Entry, flags shared.Flags) error {
	problems, err := engine.RestorePreflight(loc.Dir, nsName, entries)
	if err != nil {
		return err
	}

	purge := flags.Purge
	if len(problems) > 0 && !flags.Purge {
		if flags.Force {
			// --force resolves every occupied destination as [t]: trash the
			// occupant and restore ours.
		} else if !ui.Interactive() {
			return fmt.Errorf("cannot remove from %q non-interactively:\n%s\nrerun with --force to trash the occupant and restore, or --purge to keep it and discard %s's copy",
				nsName, renderRestoreProblems(problems), nsName)
		} else {
			choice, err := ui.Prompt(
				fmt.Sprintf("removing from %q has %d occupied destination(s):\n%s\nchoose one", nsName, len(problems), renderRestoreProblems(problems)),
				[]string{"t", "p", "c"},
			)
			if err != nil {
				return err
			}
			switch strings.ToLower(strings.TrimSpace(choice)) {
			case "t":
				// resolved as --force above.
			case "p":
				purge = true
			default:
				return nil
			}
		}
	}

	s, err := state.Read()
	if err != nil {
		return err
	}
	key := state.Key{Repo: loc.Repo.Name, Namespace: nsName}
	return engine.Restore(key, loc.Dir, entries, problems, s, purge)
}

func renderRestoreProblems(problems []engine.RestoreProblem) string {
	lines := make([]string, len(problems))
	for i, p := range problems {
		lines[i] = p.Message
	}
	return strings.Join(lines, "\n")
}

func entryByDest(m manifest.Manifest, dest string) (manifest.Entry, bool) {
	for _, e := range m.Entries {
		if e.Dest == dest {
			return e, true
		}
	}
	return manifest.Entry{}, false
}
