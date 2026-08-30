package namespace

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/paths"
)

// Add tracks path into the namespace at namespaceDir. Per concept.md
// "In-repo naming", a name already present in the namespace is three
// different situations, told apart by whether a manifest entry exists and
// whether it names the same destination:
//
//   - no entry: the payload is untracked — adopts, writing the entry and
//     linking the destination without moving anything.
//   - an entry naming the same destination: already tracked — reports and
//     stops, not an error.
//   - an entry naming a different destination: a basename collision — a
//     hard error naming both destinations.
//
// When the namespace holds no payload by that name yet, this is the
// ordinary case: the destination is moved into the namespace and a symlink
// is left in its place.
func Add(namespaceDir, path string) error {
	dest, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve absolute path for %s: %w", path, err)
	}

	protected, err := paths.IsProtectedRoot(dest)
	if err != nil {
		return err
	}
	if protected {
		return fmt.Errorf("refusing to track %s: protected roots (~, /, and the XDG roots) cannot be destinations", dest)
	}

	name := filepath.Base(dest)
	m, err := manifest.Read(namespaceDir)
	if err != nil {
		return err
	}

	if existing, ok := entryByName(m, name); ok {
		if existing.Dest == dest {
			fmt.Fprintf(os.Stderr, "%s is already tracked\n", dest)
			return nil
		}
		return fmt.Errorf("basename %q collides: %s and %s; track the parent directories or use a separate namespace", name, existing.Dest, dest)
	}

	payload := filepath.Join(namespaceDir, name)
	payloadExists, err := exists(payload)
	if err != nil {
		return err
	}
	if payloadExists {
		return adopt(namespaceDir, m, name, dest, payload)
	}

	info, err := os.Lstat(dest)
	if err != nil {
		return fmt.Errorf("stat %s: %w", dest, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is already a symlink", dest)
	}

	warnIfCoveredByExistingEntry(m, dest)

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

// adopt handles the "untracked payload" case: the namespace already holds
// payload with no manifest entry naming it, so add writes the entry and
// links dest to the existing payload, moving nothing.
func adopt(namespaceDir string, m manifest.Manifest, name, dest, payload string) error {
	warnIfCoveredByExistingEntry(m, dest)

	if err := os.Symlink(payload, dest); err != nil {
		return fmt.Errorf("create symlink %s -> %s: %w", dest, payload, err)
	}

	m.Entries = append(m.Entries, manifest.Entry{Name: name, Dest: dest})
	return manifest.Write(namespaceDir, m)
}

func entryByName(m manifest.Manifest, name string) (manifest.Entry, bool) {
	for _, e := range m.Entries {
		if e.Name == name {
			return e, true
		}
	}
	return manifest.Entry{}, false
}

func exists(path string) (bool, error) {
	if _, err := os.Lstat(path); err == nil {
		return true, nil
	} else if os.IsNotExist(err) {
		return false, nil
	} else {
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
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
