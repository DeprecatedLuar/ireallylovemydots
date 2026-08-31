// Package namespace holds namespace lifecycle primitives: creation, lookup
// across repositories, deletion, and renaming. No orchestration and no
// registry I/O — that is internal/commands/namespace.go.
package namespace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/repo"
	"github.com/DeprecatedLuar/dotz/internal/state"
	"github.com/DeprecatedLuar/dotz/internal/trash"
	"github.com/DeprecatedLuar/dotz/internal/ui"
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

// Located is one repository holding a locally materialized namespace by a
// given name.
type Located struct {
	Repo manifest.Repo
	Dir  string
}

// Resolve finds the locally materialized namespace called name among repos,
// rooted under dataDir. repoSpec, when non-empty, disambiguates directly by
// repository spec instead of searching. Ambiguity across repositories
// prompts when interactive, and errors naming every candidate otherwise, per
// concept.md "Name resolution".
func Resolve(dataDir string, repos []manifest.Repo, name, repoSpec string) (Located, error) {
	if repoSpec != "" {
		r, err := repo.Resolve(repos, repoSpec)
		if err != nil {
			return Located{}, err
		}
		dir := filepath.Join(dataDir, r.Name, name)
		if _, err := os.Stat(dir); err != nil {
			return Located{}, fmt.Errorf("namespace %q not found in repository %q", name, r.Name)
		}
		return Located{Repo: r, Dir: dir}, nil
	}

	var candidates []Located
	for _, r := range repos {
		names, err := LocalNames(filepath.Join(dataDir, r.Name))
		if err != nil {
			return Located{}, err
		}
		for _, n := range names {
			if n == name {
				candidates = append(candidates, Located{Repo: r, Dir: filepath.Join(dataDir, r.Name, name)})
			}
		}
	}

	switch len(candidates) {
	case 0:
		return Located{}, fmt.Errorf("no namespace named %q found in any registered repository", name)
	case 1:
		return candidates[0], nil
	}

	repoNames := make([]string, 0, len(candidates))
	for _, c := range candidates {
		repoNames = append(repoNames, c.Repo.Name)
	}
	if !ui.Interactive() {
		return Located{}, fmt.Errorf("namespace %q exists in multiple repositories (%s); disambiguate with --repo", name, strings.Join(repoNames, ", "))
	}

	choice, err := ui.Prompt("", fmt.Sprintf("namespace %q exists in multiple repositories. Choose one:", name), repoNames)
	if err != nil {
		return Located{}, err
	}
	for _, c := range candidates {
		if strings.EqualFold(c.Repo.Name, choice) {
			return c, nil
		}
	}
	return Located{}, fmt.Errorf("no repository named %q", choice)
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
// and no symlink — destinations live in the manifest and are unaffected by
// the namespace's name.
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
