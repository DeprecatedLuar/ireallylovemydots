package repo

import (
	"fmt"
	"path/filepath"

	"github.com/DeprecatedLuar/ireallylovemydots/internal/manifest"
)

// RootName returns the name of repoDir's root namespace, read from the
// .dots at its root: "" when there is none or it carries ignore = true.
func RootName(repoDir string) (string, error) {
	exists, err := manifest.Exists(repoDir)
	if err != nil || !exists {
		return "", err
	}
	m, err := manifest.Read(repoDir)
	if err != nil {
		return "", err
	}
	if m.Ignore {
		return "", nil
	}
	if err := manifest.CheckName(m, true); err != nil {
		return "", fmt.Errorf("%s: %w", manifest.Path(repoDir), err)
	}
	return m.Name, nil
}

// NamespaceDir returns the folder holding namespace name in repoDir: the
// repository itself for its root namespace, a top-level folder otherwise.
func NamespaceDir(repoDir, name string) (string, error) {
	root, err := RootName(repoDir)
	if err != nil {
		return "", err
	}
	if root != "" && root == name {
		return repoDir, nil
	}
	return filepath.Join(repoDir, name), nil
}

// OwnerOf returns the namespace owning top-level entry in repoDir: a folder
// holding its own .dots owns itself; with a root namespace, every other
// entry belongs to it.
func OwnerOf(repoDir, entry string) (string, error) {
	root, err := RootName(repoDir)
	if err != nil || root == "" {
		return entry, err
	}
	own, err := manifest.Exists(filepath.Join(repoDir, entry))
	if err != nil {
		return "", err
	}
	if own {
		return entry, nil
	}
	return root, nil
}
