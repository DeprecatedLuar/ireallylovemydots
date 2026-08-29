// Package manifest reads and writes the namespace manifest: the per-namespace
// record of tracked entries and the absolute destinations they belong at.
package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const fileName = ".dots"

// Entry is one tracked file or directory within a namespace.
type Entry struct {
	Name string `json:"name"`
	Dest string `json:"dest"`
}

// Manifest is a namespace's full set of tracked entries.
type Manifest struct {
	Entries []Entry `json:"entries"`
}

// Path returns the manifest file path for a namespace directory.
func Path(namespaceDir string) string {
	return filepath.Join(namespaceDir, fileName)
}

// Read loads the manifest from a namespace directory. A missing file is not
// an error: it returns an empty manifest, since a freshly created namespace
// has no manifest until its first entry.
func Read(namespaceDir string) (Manifest, error) {
	data, err := os.ReadFile(Path(namespaceDir))
	if os.IsNotExist(err) {
		return Manifest{}, nil
	}
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest %s: %w", Path(namespaceDir), err)
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("parse manifest %s: %w", Path(namespaceDir), err)
	}
	for i := range m.Entries {
		m.Entries[i].Dest = expandHome(m.Entries[i].Dest)
	}
	return m, nil
}

// Write persists the manifest, sorted by name, with destinations contracted
// to ~-relative form where applicable.
func Write(namespaceDir string, m Manifest) error {
	sorted := make([]Entry, len(m.Entries))
	copy(sorted, m.Entries)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for i := range sorted {
		sorted[i].Dest = contractHome(sorted[i].Dest)
	}

	data, err := json.MarshalIndent(Manifest{Entries: sorted}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(Path(namespaceDir), data, 0644); err != nil {
		return fmt.Errorf("write manifest %s: %w", Path(namespaceDir), err)
	}
	return nil
}

func expandHome(dest string) string {
	if dest == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return dest
		}
		return home
	}
	if strings.HasPrefix(dest, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return dest
		}
		return filepath.Join(home, dest[2:])
	}
	return dest
}

func contractHome(dest string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return dest
	}
	if dest == home {
		return "~"
	}
	if strings.HasPrefix(dest, home+string(filepath.Separator)) {
		return "~" + dest[len(home):]
	}
	return dest
}
