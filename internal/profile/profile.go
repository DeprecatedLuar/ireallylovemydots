package profile

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/DeprecatedLuar/dotz/internal/trash"
)

// Create declares a new profile. from is empty for an empty profile, or
// names main or another profile to seed it with copies of that version of
// every profiled entry (concept.md "Profile level"). An empty profile is
// legal: it simply overrides nothing.
func Create(namespaceDir, name, from string) error {
	m, err := Read(namespaceDir)
	if err != nil {
		return err
	}
	if m.HasProfile(name) {
		return fmt.Errorf("profile %q already exists", name)
	}
	if from != "" && from != Main && !m.HasProfile(from) {
		return fmt.Errorf("no profile named %q to copy from", from)
	}

	if from != "" {
		if err := seed(namespaceDir, m, name, from); err != nil {
			return err
		}
	}

	m.Profiles = append(m.Profiles, name)
	return Write(namespaceDir, m)
}

// seed copies from's version of every profiled entry into the new profile's
// folder. An entry from does not hold — because from is main and the root
// payload is missing, or because from overrides nothing for it — is skipped
// rather than failing the whole creation: a profile need not override every
// profiled entry.
func seed(namespaceDir string, m Manifest, name, from string) error {
	dir := ProfileDir(namespaceDir, name)
	for _, entry := range m.Entries {
		src, err := Source(namespaceDir, entry, from)
		if err != nil {
			return err
		}
		if _, statErr := os.Lstat(src); os.IsNotExist(statErr) {
			continue
		} else if statErr != nil {
			return fmt.Errorf("stat %s: %w", src, statErr)
		}
		if err := os.MkdirAll(dir, dirPerm); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
		if err := copyTree(src, filepath.Join(dir, entry)); err != nil {
			return err
		}
	}
	return nil
}

// Remove undeclares a profile and trashes whatever overrides it held.
// Relinking the destinations it was overriding is the caller's job — this
// package holds no machine state.
func Remove(namespaceDir, name string) error {
	m, err := Read(namespaceDir)
	if err != nil {
		return err
	}
	if !m.HasProfile(name) {
		return fmt.Errorf("no profile named %q", name)
	}

	dir := ProfileDir(namespaceDir, name)
	if _, statErr := os.Lstat(dir); statErr == nil {
		if _, err := trash.Move(dir); err != nil {
			return fmt.Errorf("trash profile %s: %w", dir, err)
		}
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("stat %s: %w", dir, statErr)
	}

	m.Profiles = slices.DeleteFunc(m.Profiles, func(p string) bool { return p == name })
	return Write(namespaceDir, m)
}

// Rename renames a profile and the folder holding its overrides.
func Rename(namespaceDir, oldName, newName string) error {
	m, err := Read(namespaceDir)
	if err != nil {
		return err
	}
	if !m.HasProfile(oldName) {
		return fmt.Errorf("no profile named %q", oldName)
	}
	if m.HasProfile(newName) {
		return fmt.Errorf("profile %q already exists", newName)
	}

	oldDir := ProfileDir(namespaceDir, oldName)
	newDir := ProfileDir(namespaceDir, newName)
	if _, statErr := os.Lstat(oldDir); statErr == nil {
		if err := os.Rename(oldDir, newDir); err != nil {
			return fmt.Errorf("rename profile %s to %s: %w", oldDir, newDir, err)
		}
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("stat %s: %w", oldDir, statErr)
	}

	for i, p := range m.Profiles {
		if p == oldName {
			m.Profiles[i] = newName
		}
	}
	return Write(namespaceDir, m)
}

// Declare marks an entry profiled. It moves no files and creates no links —
// it is a manifest edit (concept.md "Membership"). Whether the namespace
// manifest actually tracks the entry is checked by the caller, which is the
// side holding that manifest.
func Declare(namespaceDir, entryName string) error {
	m, err := Read(namespaceDir)
	if err != nil {
		return err
	}
	if m.HasEntry(entryName) {
		return fmt.Errorf("%q is already profiled", entryName)
	}
	m.Entries = append(m.Entries, entryName)
	return Write(namespaceDir, m)
}

// Undeclare removes an entry from the profiled set. Overrides still held for
// it are the caller's to clear first — see concept.md "Teardown".
func Undeclare(namespaceDir, entryName string) error {
	m, err := Read(namespaceDir)
	if err != nil {
		return err
	}
	if !m.HasEntry(entryName) {
		return fmt.Errorf("%q is not profiled", entryName)
	}
	m.Entries = slices.DeleteFunc(m.Entries, func(e string) bool { return e == entryName })
	return Write(namespaceDir, m)
}

// AddOverride copies the namespace root's version of an entry into a
// profile's folder. A directory entry is copied whole: the destination
// symlink points at either the profile's copy or the root one, so a profiled
// directory replaces the root one outright (concept.md "Overrides").
func AddOverride(namespaceDir, profileName, entryName string) error {
	src := filepath.Join(namespaceDir, entryName)
	if _, err := os.Lstat(src); err != nil {
		return fmt.Errorf("stat %s: %w", src, err)
	}

	dir := ProfileDir(namespaceDir, profileName)
	dst := filepath.Join(dir, entryName)
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("profile %q already overrides %q", profileName, entryName)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", dst, err)
	}

	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	return copyTree(src, dst)
}

// DropOverride trashes one profile's override of an entry. The destination
// relinking to the root copy is the caller's job.
func DropOverride(namespaceDir, profileName, entryName string) error {
	path := filepath.Join(ProfileDir(namespaceDir, profileName), entryName)
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return fmt.Errorf("profile %q does not override %q", profileName, entryName)
	} else if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if _, err := trash.Move(path); err != nil {
		return fmt.Errorf("trash override %s: %w", path, err)
	}
	return nil
}

// Overrides returns the entry names a profile's folder holds, sorted. The
// walk stops at entry level and never descends into a profiled directory,
// exactly as the namespace check does not descend into a tracked one
// (concept.md "Self-healing").
func Overrides(namespaceDir, profileName string) ([]string, error) {
	dir := ProfileDir(namespaceDir, profileName)
	dirEntries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read profile directory %s: %w", dir, err)
	}
	names := make([]string, 0, len(dirEntries))
	for _, de := range dirEntries {
		names = append(names, de.Name())
	}
	sort.Strings(names)
	return names, nil
}

// OverridingProfiles returns every declared profile holding an override for
// entryName, sorted — what concept.md "Teardown" requires naming before an
// entry can be undeclared.
func OverridingProfiles(namespaceDir, entryName string) ([]string, error) {
	m, err := Read(namespaceDir)
	if err != nil {
		return nil, err
	}
	var holding []string
	for _, p := range m.Profiles {
		path := filepath.Join(ProfileDir(namespaceDir, p), entryName)
		if _, err := os.Lstat(path); err == nil {
			holding = append(holding, p)
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("stat %s: %w", path, err)
		}
	}
	sort.Strings(holding)
	return holding, nil
}

// Folders returns the profile folders present under .profiles, sorted —
// what self-heal cross-checks against the declared profiles.
func Folders(namespaceDir string) ([]string, error) {
	dirEntries, err := os.ReadDir(Dir(namespaceDir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", Dir(namespaceDir), err)
	}
	var names []string
	for _, de := range dirEntries {
		if de.IsDir() {
			names = append(names, de.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// copyTree copies a file or a whole directory tree, preserving permissions.
func copyTree(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("stat %s: %w", src, err)
	}
	if !info.IsDir() {
		return copyFile(src, dst, info.Mode().Perm())
	}

	if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	dirEntries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	for _, de := range dirEntries {
		if err := copyTree(filepath.Join(src, de.Name()), filepath.Join(dst, de.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("copy %s to %s: %w", src, dst, err)
	}
	return out.Close()
}
