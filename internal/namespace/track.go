package namespace

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/DeprecatedLuar/dotz/internal/manifest"
)

// Add tracks path into the namespace at namespaceDir: resolves it to an
// absolute destination, derives the in-repo name from its basename, rejects
// a basename collision with an existing entry (naming both), moves the
// payload into the namespace, leaves a symlink at the original location, and
// records the manifest entry.
func Add(namespaceDir, path string) error {
	dest, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve absolute path for %s: %w", path, err)
	}
	info, err := os.Lstat(dest)
	if err != nil {
		return fmt.Errorf("stat %s: %w", dest, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is already a symlink", dest)
	}

	name := filepath.Base(dest)
	m, err := manifest.Read(namespaceDir)
	if err != nil {
		return err
	}
	for _, e := range m.Entries {
		if e.Name == name && e.Dest != dest {
			return fmt.Errorf("basename %q collides: %s and %s; track the parent directories or use a separate namespace", name, e.Dest, dest)
		}
	}

	warnIfCoveredByExistingEntry(m, dest)

	payload := filepath.Join(namespaceDir, name)
	if _, err := os.Stat(payload); err == nil {
		return fmt.Errorf("%s already exists in the namespace", payload)
	}

	if err := os.Rename(dest, payload); err != nil {
		return fmt.Errorf("move %s into namespace: %w", dest, err)
	}
	if err := os.Symlink(payload, dest); err != nil {
		if rollbackErr := os.Rename(payload, dest); rollbackErr != nil {
			return fmt.Errorf("create symlink %s: %w (rollback also failed: %v)", dest, err, rollbackErr)
		}
		return fmt.Errorf("create symlink %s -> %s: %w", dest, payload, err)
	}

	m.Entries = append(m.Entries, manifest.Entry{Name: name, Dest: dest})
	return manifest.Write(namespaceDir, m)
}

// warnIfCoveredByExistingEntry warns, without blocking, when dest falls
// beneath an existing entry's destination in the same namespace — the case
// concept.md flags as a namespace modelled wrong. The in-repo link guard in
// the link engine (phase 5) is the actual enforcement mechanism; this is
// only an early heads-up at track time.
func warnIfCoveredByExistingEntry(m manifest.Manifest, dest string) {
	for _, e := range m.Entries {
		if e.Dest == dest {
			continue
		}
		if rel, err := filepath.Rel(e.Dest, dest); err == nil && rel != "." && !hasParentPrefix(rel) {
			fmt.Fprintf(os.Stderr, "warning: %s is already covered by the entry for %s\n", dest, e.Dest)
		}
	}
}

func hasParentPrefix(rel string) bool {
	return len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator)
}
