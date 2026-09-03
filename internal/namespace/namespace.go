// Package namespace holds namespace lifecycle primitives: creation, lookup
// across repositories, deletion, and renaming. No orchestration and no
// registry I/O — that is internal/commands/namespace.go.
package namespace

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/repo"
	"github.com/DeprecatedLuar/dotz/internal/state"
	"github.com/DeprecatedLuar/dotz/internal/trash"
)

const dirPerm = 0755

// Create makes an empty namespace folder with an empty manifest inside
// repoDir. It errors if a namespace by that name already exists on disk.
func Create(repoDir, name string) (string, error) {
	dir := filepath.Join(repoDir, name)
	if _, err := os.Stat(dir); err == nil {
		return "", fmt.Errorf("namespace %q already exists in %s", name, repoDir)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat %s: %w", dir, err)
	}

	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return "", fmt.Errorf("create namespace %s: %w", dir, err)
	}
	if err := manifest.Write(dir, manifest.Manifest{}); err != nil {
		return "", fmt.Errorf("initialize manifest for namespace %s: %w", dir, err)
	}
	return dir, nil
}

// LocalNames returns the namespace folders materialized on disk directly
// inside repoDir: top-level directories other than dotfiles like ".git". A
// namespace need not be committed to appear here — creation and tracking
// write straight to the worktree, ahead of sync.
func LocalNames(repoDir string) ([]string, error) {
	entries, err := os.ReadDir(repoDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read repository directory %s: %w", repoDir, err)
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		names = append(names, e.Name())
	}
	return names, nil
}

// Located is one repository holding a namespace by a given name — either
// materialized on disk (Installed) or known only through the repository's
// git catalogue, per concept.md "Install and uninstall"'s `=` state.
type Located struct {
	Repo      manifest.Repo
	Dir       string
	Installed bool
}

// Resolve finds the namespace called name among repos, rooted under
// dataDir: a locally materialized folder first, falling back to a
// repository's git catalogue for a namespace that has never been checked
// out on this machine — the `=` state install/enable -i exist to fetch.
// repoSpec, when non-empty, disambiguates directly by repository spec
// instead of searching, checking that repository's catalogue the same way
// rather than deciding existence with a bare stat. Ambiguity across
// repositories is a name the user typed matching more than one existing
// thing, per concept.md "Name resolution": it errors, naming every
// candidate and the repository it comes from, and never prompts, even
// interactively.
func Resolve(dataDir string, repos []manifest.Repo, name, repoSpec string) (Located, error) {
	if repoSpec != "" {
		r, err := repo.Resolve(repos, repoSpec)
		if err != nil {
			return Located{}, err
		}
		dir := filepath.Join(dataDir, r.Name, name)
		if _, err := os.Stat(dir); err == nil {
			return Located{Repo: r, Dir: dir, Installed: true}, nil
		}
		catalogue, err := repo.Namespaces(filepath.Join(dataDir, r.Name))
		if err != nil {
			return Located{}, err
		}
		if slices.Contains(catalogue, name) {
			return Located{Repo: r, Dir: dir}, nil
		}
		return Located{}, fmt.Errorf("namespace %q not found in repository %q", name, r.Name)
	}

	candidates, err := findCandidates(dataDir, repos, name)
	if err != nil {
		return Located{}, err
	}

	switch len(candidates) {
	case 0:
		return Located{}, fmt.Errorf("no namespace named %q found in any registered repository", name)
	case 1:
		return candidates[0], nil
	}

	return Located{}, ambiguityError(name, candidates)
}

// findCandidates locates every repository that holds a namespace called
// name, rooted under dataDir: locally materialized folders first, falling
// back to every repository's git catalogue only when nothing is
// materialized anywhere — the same two-pass search Resolve uses when no
// repoSpec pins the search to one repository.
func findCandidates(dataDir string, repos []manifest.Repo, name string) ([]Located, error) {
	var candidates []Located
	for _, r := range repos {
		names, err := LocalNames(filepath.Join(dataDir, r.Name))
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			if n == name {
				candidates = append(candidates, Located{Repo: r, Dir: filepath.Join(dataDir, r.Name, name), Installed: true})
			}
		}
	}

	if len(candidates) == 0 {
		// No locally materialized folder anywhere: fall back to every
		// repository's git catalogue for a namespace that has never been
		// checked out on this machine.
		for _, r := range repos {
			names, err := repo.Namespaces(filepath.Join(dataDir, r.Name))
			if err != nil {
				return nil, err
			}
			for _, n := range names {
				if n == name {
					candidates = append(candidates, Located{Repo: r, Dir: filepath.Join(dataDir, r.Name, name)})
				}
			}
		}
	}
	return candidates, nil
}

// Candidates reports the repositories holding a namespace called name,
// rooted under dataDir, for a caller (the router's ambiguity handling) that
// only needs to know whether the name is ambiguous by itself, before it
// decides anything else.
func Candidates(dataDir string, repos []manifest.Repo, name string) ([]manifest.Repo, error) {
	located, err := findCandidates(dataDir, repos, name)
	if err != nil {
		return nil, err
	}
	out := make([]manifest.Repo, len(located))
	for i, c := range located {
		out[i] = c.Repo
	}
	return out, nil
}

// ambiguityError names every candidate repository and the --repo flag to
// disambiguate, per concept.md "Name resolution".
func ambiguityError(name string, candidates []Located) error {
	repoNames := make([]string, 0, len(candidates))
	for _, c := range candidates {
		repoNames = append(repoNames, c.Repo.Name)
	}
	return fmt.Errorf("namespace %q exists in multiple repositories (%s); disambiguate with --repo", name, strings.Join(repoNames, ", "))
}

// Delete trashes a namespace's folder, unconditionally. Callers are
// responsible for having restored or trashed its entries first; Delete
// itself does not touch destinations.
func Delete(dir string) error {
	if _, err := trash.Move(dir); err != nil {
		return fmt.Errorf("trash namespace %s: %w", dir, err)
	}
	return nil
}

// Rename moves a namespace's folder to newName within the same repository
// and, if a state entry already exists under the old key (the namespace was
// enabled), carries it forward under the new key. It touches no destination
// — destinations live in the manifest and are unaffected by the namespace's
// name — but every symlink dots creates targets an absolute path that
// includes the namespace's own name, so a rename does move every one of its
// links' targets. Rename itself does not repoint them; the caller
// (internal/commands' renameNamespace) does that immediately afterward via
// engine.Relink, so a namespace never sits with dangling links between the
// two steps.
func Rename(repoDir, repoName, oldName, newName string) error {
	oldDir := filepath.Join(repoDir, oldName)
	newDir := filepath.Join(repoDir, newName)

	if _, err := os.Stat(oldDir); err != nil {
		return fmt.Errorf("namespace %q not found in %s", oldName, repoDir)
	}
	if _, err := os.Stat(newDir); err == nil {
		return fmt.Errorf("namespace %q already exists in %s", newName, repoDir)
	}

	if err := os.Rename(oldDir, newDir); err != nil {
		return fmt.Errorf("rename namespace %s to %s: %w", oldDir, newDir, err)
	}

	return renameState(repoName, oldName, newName)
}

func renameState(repoName, oldName, newName string) error {
	s, err := state.Read()
	if err != nil {
		return err
	}
	oldKey := state.Key{Repo: repoName, Namespace: oldName}
	entry, ok := s.Entries[oldKey]
	if !ok {
		return nil
	}
	delete(s.Entries, oldKey)
	s.Entries[state.Key{Repo: repoName, Namespace: newName}] = entry
	return state.Write(s)
}
