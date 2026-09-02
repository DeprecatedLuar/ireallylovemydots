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

// DestNone is the literal destination value marking an entry deliberately
// out of scope for linking: still tracked and still checked for its payload
// (namespace.Inspect's orphan check applies to it like any other entry), but
// never symlinked and never reported invalid — unlike an empty Dest, which
// concept.md "Manual edits" treats as an unfinished entry that must be given
// a real destination or removed.
const DestNone = "none"

// Entry is one tracked file or directory within a namespace.
type Entry struct {
	Name string `toml:"name"`
	Dest string `toml:"dest"`
}

// HasDestination reports whether e names a real destination to link — false
// for an empty Dest (invalid, concept.md "Manual edits") and for DestNone
// (deliberately unlinked), the two cases every linking path must treat
// alike: nothing to create, repoint, or count toward "every entry linked."
func (e Entry) HasDestination() bool {
	return e.Dest != "" && e.Dest != DestNone
}

// Manifest is a namespace's full set of tracked entries.
type Manifest struct {
	// Ignore marks the namespace explicitly out of dots' scope — invisible
	// to listing and self-healing, per concept.md "Namespace". Set and
	// cleared by `namespace ignore`/`unignore`, never by hand-editing alone.
	Ignore  bool    `toml:"ignore,omitempty"`
	Entries []Entry `toml:"entries"`
}

// Path returns the manifest file path for a namespace directory.
func Path(namespaceDir string) string {
	return filepath.Join(namespaceDir, fileName)
}

// Exists reports whether a namespace directory has a manifest file at all —
// the distinction Read deliberately erases (a missing file and an empty
// manifest both read back as Manifest{}), needed wherever "no .dots" and
// "a .dots with zero entries" must be told apart.
func Exists(namespaceDir string) (bool, error) {
	_, err := os.Stat(Path(namespaceDir))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat manifest %s: %w", Path(namespaceDir), err)
	}
	return true, nil
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
	return toml.Marshal(Manifest{Ignore: m.Ignore, Entries: sorted})
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
