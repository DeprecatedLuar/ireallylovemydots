// Package profile holds the per-namespace profile layer: the .profiles
// manifest, the override copies beside it, and the single resolution rule
// deciding which version of an entry a destination links to. No
// orchestration and no machine state — the active profile lives in
// internal/state, and sequencing belongs to internal/commands.
package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/BurntSushi/toml"
)

const (
	// DirName is the folder holding the profile manifest and every profile's
	// override copies, per concept.md "The profile manifest".
	DirName = ".profiles"
	// Main names the unprofiled layer — the namespace root. It is not a
	// profile: it cannot be enabled, created, renamed or removed, and machine
	// state encodes it as an empty active profile rather than this name
	// (concept.md "main"). It is accepted only as a membership target and as
	// a --from copy source.
	Main = "main"

	fileName = ".dots"
	dirPerm  = 0755
	filePerm = 0644
)

// Manifest is .profiles/.dots: the profiles a namespace offers and the
// entries allowed to have overrides. Both are declared rather than derived —
// a profile that overrides nothing has no folder, and git does not carry
// empty directories (concept.md "The profile manifest").
type Manifest struct {
	Profiles []string `toml:"profiles"`
	Entries  []string `toml:"entries"`
}

// HasProfile reports whether name is declared in profiles.
func (m Manifest) HasProfile(name string) bool {
	return slices.Contains(m.Profiles, name)
}

// HasEntry reports whether name is declared profiled.
func (m Manifest) HasEntry(name string) bool {
	return slices.Contains(m.Entries, name)
}

// Dir returns a namespace's .profiles directory.
func Dir(namespaceDir string) string {
	return filepath.Join(namespaceDir, DirName)
}

// Path returns a namespace's profile manifest file.
func Path(namespaceDir string) string {
	return filepath.Join(Dir(namespaceDir), fileName)
}

// ProfileDir returns the folder holding one profile's override copies. The
// folder need not exist: a profile that overrides nothing has none.
func ProfileDir(namespaceDir, name string) string {
	return filepath.Join(Dir(namespaceDir), name)
}

// Read loads a namespace's profile manifest. A missing file is not an error:
// it reads as an empty manifest, the same convention internal/manifest uses,
// since a namespace with no profiles has no .profiles folder at all.
func Read(namespaceDir string) (Manifest, error) {
	data, err := os.ReadFile(Path(namespaceDir))
	if os.IsNotExist(err) {
		return Manifest{}, nil
	}
	if err != nil {
		return Manifest{}, fmt.Errorf("read profile manifest %s: %w", Path(namespaceDir), err)
	}
	var m Manifest
	if err := toml.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("parse profile manifest %s: %w", Path(namespaceDir), err)
	}
	return m, nil
}

// Write persists a namespace's profile manifest with both arrays sorted, so
// two machines adding different things between syncs merge cleanly
// (concept.md "The profile manifest").
func Write(namespaceDir string, m Manifest) error {
	if err := os.MkdirAll(Dir(namespaceDir), dirPerm); err != nil {
		return fmt.Errorf("create %s: %w", Dir(namespaceDir), err)
	}
	data, err := Encode(m)
	if err != nil {
		return err
	}
	if err := os.WriteFile(Path(namespaceDir), data, filePerm); err != nil {
		return fmt.Errorf("write profile manifest %s: %w", Path(namespaceDir), err)
	}
	return nil
}

// Encode marshals a profile manifest to TOML with both arrays sorted.
func Encode(m Manifest) ([]byte, error) {
	sorted := Manifest{
		Profiles: append([]string{}, m.Profiles...),
		Entries:  append([]string{}, m.Entries...),
	}
	sort.Strings(sorted.Profiles)
	sort.Strings(sorted.Entries)
	data, err := toml.Marshal(sorted)
	if err != nil {
		return nil, fmt.Errorf("encode profile manifest: %w", err)
	}
	return data, nil
}
