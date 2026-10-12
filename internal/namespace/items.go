package namespace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeprecatedLuar/ireallylovemydots/internal/git"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/manifest"
)

// gitDir is the repository's own git directory, reserved inside the root
// namespace.
const gitDir = ".git"

// reserved names every namespace folder holds as dots' own plumbing, never
// as payload: the manifest, a namespace's .gitignore, and the profile layer.
var reserved = map[string]bool{
	dotsManifestFile: true,
	gitignoreFile:    true,
	profilesDir:      true,
}

// Items returns the payload names in nsDir: every entry except the
// reserved names and, for the root namespace, .git and every folder holding
// its own .dots.
func Items(nsDir string, root bool) ([]string, error) {
	dirEntries, err := os.ReadDir(nsDir)
	if err != nil {
		return nil, fmt.Errorf("read namespace directory %s: %w", nsDir, err)
	}
	var items []string
	for _, de := range dirEntries {
		skip, err := excluded(nsDir, de.Name(), root)
		if err != nil {
			return nil, err
		}
		if !skip {
			items = append(items, de.Name())
		}
	}
	return items, nil
}

// excluded reports whether top-level entry name of nsDir is never payload:
// a reserved name or, for the root namespace, .git or a folder holding its
// own .dots.
func excluded(nsDir, name string, root bool) (bool, error) {
	if reserved[name] {
		return true, nil
	}
	if !root {
		return false, nil
	}
	if name == gitDir {
		return true, nil
	}
	path := filepath.Join(nsDir, name)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.IsDir() {
		return false, nil
	}
	return manifest.Exists(path)
}

// TreeItems returns the payload names of folder namespace name as committed
// at HEAD, for a namespace not materialized on disk.
func TreeItems(repoDir, name string) ([]string, error) {
	paths, err := git.ListTree(repoDir, "HEAD", name)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var items []string
	for _, p := range paths {
		first, _, _ := strings.Cut(filepath.ToSlash(p), "/")
		if reserved[first] || seen[first] {
			continue
		}
		seen[first] = true
		items = append(items, first)
	}
	return items, nil
}

// Linkable returns every entry the namespace at nsDir links: its manifest
// entries plus, under a default destination, one per untracked item. A root
// namespace's entries naming an excluded entry are dropped.
func Linkable(nsDir string, m manifest.Manifest) ([]manifest.Entry, error) {
	root := m.Name != ""
	if root {
		kept := make([]manifest.Entry, 0, len(m.Entries))
		for _, e := range m.Entries {
			head, _, _ := strings.Cut(filepath.ToSlash(e.Name), "/")
			skip, err := excluded(nsDir, head, root)
			if err != nil {
				return nil, err
			}
			if !skip {
				kept = append(kept, e)
			}
		}
		m.Entries = kept
	}
	if m.Dest == "" {
		return m.Entries, nil
	}
	items, err := Items(nsDir, root)
	if err != nil {
		return nil, err
	}
	return manifest.Expand(m, items), nil
}
