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
		name := de.Name()
		if reserved[name] {
			continue
		}
		if root {
			if name == gitDir {
				continue
			}
			if de.IsDir() {
				held, err := manifest.Exists(filepath.Join(nsDir, name))
				if err != nil {
					return nil, err
				}
				if held {
					continue
				}
			}
		}
		items = append(items, name)
	}
	return items, nil
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
// entries plus, under a default destination, one per untracked item.
func Linkable(nsDir string, m manifest.Manifest) ([]manifest.Entry, error) {
	if m.Dest == "" {
		return m.Entries, nil
	}
	items, err := Items(nsDir, m.Name != "")
	if err != nil {
		return nil, err
	}
	return manifest.Expand(m, items), nil
}
