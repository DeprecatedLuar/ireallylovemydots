package repo

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/DeprecatedLuar/ireallylovemydots/internal/grammar"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/manifest"
)

// planHeader opens every bootstrap plan buffer.
const planHeader = `# [root] entries stay where they are, tracked by the root namespace named
# below; [namespaces] entries each move into a namespace folder of their own.
# dest is the root namespace's default destination. A deleted line leaves
# that entry in place, untracked unless dest covers it.
`

// BootstrapPlan is the editable --bootstrap plan, destinations as written
// in the buffer.
type BootstrapPlan struct {
	Name       string            `toml:"name"`
	Dest       string            `toml:"dest"`
	Root       map[string]string `toml:"root"`
	Namespaces map[string]string `toml:"namespaces"`
}

// DefaultBootstrapPlan keeps every root entry at the root under a root
// namespace named after the repository: furniture as "none", everything
// else at concept.md "Bootstrap"'s default destination.
func DefaultBootstrapPlan(repoName string, entries []RootEntry) (BootstrapPlan, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return BootstrapPlan{}, fmt.Errorf("resolve home directory: %w", err)
	}
	p := BootstrapPlan{Name: repoName, Root: map[string]string{}, Namespaces: map[string]string{}}
	for _, e := range plannable(entries) {
		if isFurniture(e.Name) {
			p.Root[e.Name] = manifest.DestNone
			continue
		}
		_, dest := destinationFor(home, e.Name, e.IsDir)
		p.Root[e.Name] = manifest.ContractHome(dest)
	}
	return p, nil
}

// plannable is every root entry a plan may list.
func plannable(entries []RootEntry) []RootEntry {
	var out []RootEntry
	for _, e := range entries {
		if e.HasDots || neverPlanned[e.Name] {
			continue
		}
		out = append(out, e)
	}
	return out
}

// EncodeBootstrapPlan renders p as the editor buffer, keys always quoted.
func EncodeBootstrapPlan(p BootstrapPlan) []byte {
	var b strings.Builder
	b.WriteString(planHeader)
	fmt.Fprintf(&b, "name = %s\ndest = %s\n", strconv.Quote(p.Name), strconv.Quote(p.Dest))
	writeTable(&b, "root", p.Root)
	writeTable(&b, "namespaces", p.Namespaces)
	return []byte(b.String())
}

func writeTable(b *strings.Builder, name string, rows map[string]string) {
	fmt.Fprintf(b, "\n[%s]\n", name)
	keys := make([]string, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(b, "%s = %s\n", strconv.Quote(k), strconv.Quote(rows[k]))
	}
}

// DecodeBootstrapPlan parses an edited buffer and validates it against the
// repository's root entries.
func DecodeBootstrapPlan(data []byte, entries []RootEntry) (BootstrapPlan, error) {
	var p BootstrapPlan
	meta, err := toml.Decode(string(data), &p)
	if err != nil {
		return BootstrapPlan{}, err
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return BootstrapPlan{}, fmt.Errorf("unknown key %q", undecoded[0].String())
	}
	return p, p.validate(entries)
}

func (p BootstrapPlan) validate(entries []RootEntry) error {
	known := map[string]RootEntry{}
	folders := map[string]bool{}
	for _, e := range plannable(entries) {
		known[e.Name] = e
	}
	for _, e := range entries {
		if e.HasDots {
			folders[e.Name] = true
		}
	}

	for name, dest := range p.Root {
		if _, ok := known[name]; !ok {
			return fmt.Errorf("[root] %q is not a root entry of this repository", name)
		}
		if _, both := p.Namespaces[name]; both {
			return fmt.Errorf("%q is in both [root] and [namespaces]", name)
		}
		if dest == "" {
			return fmt.Errorf("[root] %q has no destination; give one or \"none\"", name)
		}
	}
	if p.Dest != "" || len(p.Root) > 0 {
		if err := manifest.CheckName(manifest.Manifest{Name: p.Name}, true); err != nil {
			return err
		}
	}

	source := map[string]string{}
	for name, dest := range p.Namespaces {
		e, ok := known[name]
		if !ok {
			return fmt.Errorf("[namespaces] %q is not a root entry of this repository", name)
		}
		if dest == "" {
			return fmt.Errorf("[namespaces] %q has no destination", name)
		}
		ns := namespaceNameFor(name, e.IsDir)
		if grammar.IsReserved(ns) {
			return fmt.Errorf("%q would become namespace %q, a reserved word", name, ns)
		}
		if other, dup := source[ns]; dup {
			return fmt.Errorf("%q and %q both resolve to namespace %q", other, name, ns)
		}
		if folders[ns] {
			return fmt.Errorf("%q would become namespace %q, which already exists", name, ns)
		}
		if _, moving := p.Namespaces[ns]; ns != name && !moving {
			if _, stays := known[ns]; stays {
				return fmt.Errorf("%q would become namespace %q, which is a root entry that stays", name, ns)
			}
		}
		source[ns] = name
	}
	return nil
}

// Moves returns the [namespaces] entries as conversions, sorted by
// namespace.
func (p BootstrapPlan) Moves(entries []RootEntry) []PlannedNamespace {
	isDir := map[string]bool{}
	for _, e := range entries {
		isDir[e.Name] = e.IsDir
	}
	moves := make([]PlannedNamespace, 0, len(p.Namespaces))
	for name, dest := range p.Namespaces {
		moves = append(moves, PlannedNamespace{
			Namespace: namespaceNameFor(name, isDir[name]),
			EntryName: name,
			Dest:      manifest.ExpandHome(dest),
		})
	}
	sort.Slice(moves, func(i, j int) bool { return moves[i].Namespace < moves[j].Namespace })
	return moves
}

// ApplyBootstrap performs p at repoPath: moves the [namespaces] entries,
// then writes the root .dots when the plan names a root namespace, listing
// only the [root] entries the default destination does not already cover.
func ApplyBootstrap(repoPath string, p BootstrapPlan, entries []RootEntry) error {
	if err := Apply(repoPath, p.Moves(entries)); err != nil {
		return err
	}
	if p.Dest == "" && len(p.Root) == 0 {
		return nil
	}
	m := manifest.Manifest{Name: p.Name, Dest: manifest.ExpandHome(p.Dest)}
	for name, raw := range p.Root {
		dest := raw
		if dest != manifest.DestNone {
			dest = manifest.ExpandHome(raw)
		}
		if m.Dest != "" && dest == filepath.Join(m.Dest, name) {
			continue
		}
		m.Entries = append(m.Entries, manifest.Entry{Name: name, Dest: dest})
	}
	return manifest.Write(repoPath, m)
}
