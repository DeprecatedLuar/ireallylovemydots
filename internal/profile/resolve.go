package profile

import (
	"fmt"
	"os"
	"path/filepath"
)

// Source returns the path an entry's destination symlink must point at:
// the active profile's override when there is one, the namespace root
// otherwise. That is the whole resolution model — main, with the active
// profile's overrides on top, per entry, with no third layer and no chain of
// fallbacks (concept.md "Resolution").
//
// An empty activeProfile is main, and always resolves to the root.
//
// An override counts only when the entry is declared profiled: an
// undeclared file inside a profile folder is drift for self-heal to warn
// about (concept.md "Self-healing"), and silently linking it would put a
// file nothing declared at a destination.
func Source(namespaceDir, entryName, activeProfile string) (string, error) {
	root := filepath.Join(namespaceDir, entryName)
	if activeProfile == "" || activeProfile == Main {
		return root, nil
	}

	m, err := Read(namespaceDir)
	if err != nil {
		return "", err
	}
	if !m.HasEntry(entryName) {
		return root, nil
	}

	override := filepath.Join(ProfileDir(namespaceDir, activeProfile), entryName)
	switch _, err := os.Lstat(override); {
	case err == nil:
		return override, nil
	case os.IsNotExist(err):
		return root, nil
	default:
		return "", fmt.Errorf("stat override %s: %w", override, err)
	}
}
