package manifest

import (
	"fmt"

	"github.com/DeprecatedLuar/dotz/internal/paths"
)

// Problem is one way a manifest, though syntactically valid TOML, does not
// make sense as a set of tracked entries. Entry names the entry the problem
// is reported against; Other names the entry it conflicts with.
type Problem struct {
	Entry  string
	Other  string
	Detail string
}

// Validate checks a decoded manifest for the ways it can parse cleanly and
// still be wrong, per concept.md "Manual edits" and "The in-repo link
// guard": two entries naming the same destination, or one entry's
// destination containing another's, are the in-repo link guard's
// manifest-detectable half — catchable from the manifest alone, before any
// link exists, rather than left for pre-flight to discover one enable later.
// Both entries in a bad pair are reported, not just the one whose own
// destination is unsafe to create: concept.md "The guard makes such a
// namespace fail safely, not work" — the namespace is the atomic unit, so
// every entry a human would need to look at to fix the pair gets its own
// row. A duplicate entry name is checked alongside them since it is exactly
// as cheap and just as unreadable by every by-name lookup.
//
// Only entries with a real destination (Entry.HasDestination) participate in
// the destination checks — an empty destination is already "invalid, not
// pending" and reported by the listing on its own, and DestNone is
// deliberately unlinked, so neither can collide with anything. Pure and
// side-effect free: no filesystem access, so it runs equally over a manifest
// just read from disk and one still sitting in an editor buffer.
func Validate(m Manifest) []Problem {
	var problems []Problem

	byName := map[string]int{}
	for _, e := range m.Entries {
		byName[e.Name]++
	}
	for _, e := range m.Entries {
		if byName[e.Name] > 1 {
			problems = append(problems, Problem{Entry: e.Name, Other: e.Name,
				Detail: fmt.Sprintf("duplicate entry name %q", e.Name)})
		}
	}

	for i, a := range m.Entries {
		if !a.HasDestination() {
			continue
		}
		for j := i + 1; j < len(m.Entries); j++ {
			b := m.Entries[j]
			if !b.HasDestination() {
				continue
			}
			switch {
			case a.Dest == b.Dest:
				problems = append(problems,
					Problem{Entry: a.Name, Other: b.Name, Detail: fmt.Sprintf("destination also claimed by %q", b.Name)},
					Problem{Entry: b.Name, Other: a.Name, Detail: fmt.Sprintf("destination also claimed by %q", a.Name)},
				)
			case paths.Contains(a.Dest, b.Dest):
				problems = append(problems,
					Problem{Entry: a.Name, Other: b.Name, Detail: fmt.Sprintf("contains %q's destination %s", b.Name, ContractHome(b.Dest))},
					Problem{Entry: b.Name, Other: a.Name, Detail: fmt.Sprintf("destination falls inside %q's destination %s", a.Name, ContractHome(a.Dest))},
				)
			case paths.Contains(b.Dest, a.Dest):
				problems = append(problems,
					Problem{Entry: b.Name, Other: a.Name, Detail: fmt.Sprintf("contains %q's destination %s", a.Name, ContractHome(a.Dest))},
					Problem{Entry: a.Name, Other: b.Name, Detail: fmt.Sprintf("destination falls inside %q's destination %s", b.Name, ContractHome(b.Dest))},
				)
			}
		}
	}

	return problems
}
