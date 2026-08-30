// Package manifest reads and writes the namespace manifest: the per-namespace
// record of tracked entries and the absolute destinations they belong at.
package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

const fileName = ".dots"

// Entry is one tracked file or directory within a namespace.
type Entry struct {
	Name string `toml:"name"`
	Dest string `toml:"dest"`
}

// Manifest is a namespace's full set of tracked entries.
type Manifest struct {
	Entries []Entry `toml:"entries"`
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
	m, err := Decode(data)
	if err != nil {
		return Manifest{}, fmt.Errorf("parse manifest %s: %w", Path(namespaceDir), err)
	}
	return m, nil
}

// Write persists the manifest, sorted by name, with destinations contracted
// to ~-relative form where applicable.
func Write(namespaceDir string, m Manifest) error {
	data, err := Encode(m)
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	if err := os.WriteFile(Path(namespaceDir), data, 0644); err != nil {
		return fmt.Errorf("write manifest %s: %w", Path(namespaceDir), err)
	}
	return nil
}

// Decode parses TOML manifest data, expanding ~-relative destinations to
// absolute paths. Used both by Read and by callers that hold manifest bytes
// off disk, such as namespace <ns> edit's buffer.
func Decode(data []byte) (Manifest, error) {
	var m Manifest
	if err := toml.Unmarshal(data, &m); err != nil {
		return Manifest{}, err
	}
	for i := range m.Entries {
		m.Entries[i].Dest = expandHome(m.Entries[i].Dest)
	}
	return m, nil
}

// Encode marshals a manifest to TOML, sorted by name with destinations
// contracted to ~-relative form. Used both by Write and by callers that need
// the bytes without writing them to a namespace's own .dots file, such as
// namespace <ns> edit's buffer.
func Encode(m Manifest) ([]byte, error) {
	sorted := make([]Entry, len(m.Entries))
	copy(sorted, m.Entries)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for i := range sorted {
		sorted[i].Dest = contractHome(sorted[i].Dest)
	}
	return toml.Marshal(Manifest{Entries: sorted})
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
