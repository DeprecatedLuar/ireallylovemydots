// Package engine is the link engine: the claimed-destination index,
// pre-flight checks, and the enable/disable mechanics that act on their
// results. Orchestration of a whole enable or disable call belongs to
// internal/commands; this package holds the logic those commands call into.
package engine

import (
	"path/filepath"

	"github.com/DeprecatedLuar/dotz/internal/state"
)

// claim is one destination currently linked by an enabled namespace.
type claim struct {
	dest string
	key  state.Key
}

// Index is the claimed-destination index built from machine state: every
// destination linked by a currently enabled namespace, across every
// repository.
type Index struct {
	claims []claim
}

// BuildIndex builds the claimed-destination index from state, per
// concept.md "Conflicts": every destination recorded against an enabled
// namespace, regardless of which repository it belongs to.
func BuildIndex(s state.State) Index {
	var idx Index
	for key, entry := range s.Entries {
		if !entry.Enabled {
			continue
		}
		for _, dest := range entry.LinkedDests {
			idx.claims = append(idx.claims, claim{dest: dest, key: key})
		}
	}
	return idx
}

// Conflict reports the key of the enabled namespace that already claims
// dest, if any. The comparison is prefix-aware in both directions, since a
// directory entry claims its whole subtree: "~/.config/nvim" conflicts with
// "~/.config/nvim/init.lua" regardless of which is already claimed.
func (idx Index) Conflict(dest string) (state.Key, bool) {
	for _, c := range idx.claims {
		if overlaps(c.dest, dest) {
			return c.key, true
		}
	}
	return state.Key{}, false
}

func overlaps(a, b string) bool {
	return contains(a, b) || contains(b, a)
}

// contains reports whether child is parent itself, or falls beneath it.
func contains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !hasParentPrefix(rel)
}

func hasParentPrefix(rel string) bool {
	return len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator)
}
