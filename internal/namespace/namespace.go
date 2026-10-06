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

	"github.com/DeprecatedLuar/ireallylovemydots/internal/manifest"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/repo"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/state"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/trash"
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
// git catalogue, per concept.md "Install and uninstall"'s `=` state. Name
// is the bare namespace name, whatever spec the user typed.
type Located struct {
	Repo      manifest.Repo
	Name      string
	Dir       string
	Installed bool
}

// SplitSpec splits a namespace spec at its last "/": the repository spec
// before it, the namespace name after. A bare name has no repository spec.
func SplitSpec(spec string) (repoSpec, name string) {
	i := strings.LastIndex(spec, "/")
	if i < 0 {
		return "", spec
	}
	return spec[:i], spec[i+1:]
}

// Resolve finds the namespace named by spec among repos, rooted under
// dataDir. spec is a bare name, repo/name, or owner/repo/name; a repository
// spec pins the search to that repository, as does repoFlag, and the two
// must agree. A namespace is a locally materialized folder or an entry in
// the repository's git catalogue — the `=` state install/enable -i exist to
// fetch. A bare name matching a namespace in more than one repository,
// installed or not, is ambiguous per concept.md "Name resolution": it
// errors, naming every candidate as a spec, and never prompts, even
// interactively.
func Resolve(dataDir string, repos []manifest.Repo, spec, repoFlag string) (Located, error) {
	repoSpec, name := SplitSpec(spec)
	if name == "" || (repoSpec == "" && strings.Contains(spec, "/")) {
		return Located{}, fmt.Errorf("%q is not a namespace: use name, repo/name, or owner/repo/name", spec)
	}
	if repoSpec != "" && repoFlag != "" {
		a, err := repo.Resolve(repos, repoSpec)
		if err != nil {
			return Located{}, err
		}
		b, err := repo.Resolve(repos, repoFlag)
		if err != nil {
			return Located{}, err
		}
		if a.Name != b.Name {
			return Located{}, fmt.Errorf("%q names repository %q but --repo names %q", spec, a.Name, b.Name)
		}
	}
	if repoSpec == "" {
		repoSpec = repoFlag
	}

	if repoSpec != "" {
		r, err := repo.Resolve(repos, repoSpec)
		if err != nil {
			return Located{}, err
		}
		dir := filepath.Join(dataDir, r.Name, name)
		if _, err := os.Stat(dir); err == nil {
			// Installed namespaces are always exposed, whatever the
			// whitelist says.
			return Located{Repo: r, Name: name, Dir: dir, Installed: true}, nil
		}
		catalogue, err := repo.Namespaces(filepath.Join(dataDir, r.Name))
		if err != nil {
			return Located{}, err
		}
		if slices.Contains(catalogue, name) {
			if !r.Allows(name, false) {
				return Located{}, whitelistError(name, r)
			}
			return Located{Repo: r, Name: name, Dir: dir}, nil
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
// name, rooted under dataDir: one candidate per repository, installed when
// its folder is materialized and catalogue-only when git lists it but it has
// never been checked out here.
func findCandidates(dataDir string, repos []manifest.Repo, name string) ([]Located, error) {
	var candidates []Located
	for _, r := range repos {
		repoDir := filepath.Join(dataDir, r.Name)
		local, err := LocalNames(repoDir)
		if err != nil {
			return nil, err
		}
		dir := filepath.Join(repoDir, name)
		if slices.Contains(local, name) {
			if r.Allows(name, true) {
				candidates = append(candidates, Located{Repo: r, Name: name, Dir: dir, Installed: true})
			}
			continue
		}
		catalogue, err := repo.Namespaces(repoDir)
		if err != nil {
			return nil, err
		}
		if slices.Contains(catalogue, name) && r.Allows(name, false) {
			candidates = append(candidates, Located{Repo: r, Name: name, Dir: dir})
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

// whitelistError reports that name exists in r but is filtered out by its
// namespaces whitelist — naming the filter rather than claiming the
// namespace does not exist, with a tip pointing at the registry file so the
// whitelist is easy to find and edit.
func whitelistError(name string, r manifest.Repo) error {
	msg := fmt.Sprintf("namespace %q exists in repository %q but is not in its namespaces whitelist", name, r.Name)
	path, err := manifest.RegistryPath()
	if err != nil {
		return fmt.Errorf("%s", msg)
	}
	return fmt.Errorf("%s; edit the whitelist in %s", msg, path)
}

// ambiguityError names every candidate as a namespace spec, per concept.md
// "Name resolution".
func ambiguityError(name string, candidates []Located) error {
	specs := make([]string, 0, len(candidates))
	for _, c := range candidates {
		specs = append(specs, c.Repo.Name+"/"+name)
	}
	return fmt.Errorf("%q names a namespace in %d repositories (%s); name one, e.g. `%s`",
		name, len(candidates), strings.Join(specs, ", "), specs[0])
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
