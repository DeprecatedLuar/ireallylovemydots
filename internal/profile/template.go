package profile

import (
	"fmt"
	"sort"
	"strings"
)

// EditBuffer builds the buffer `profiles edit` seeds $EDITOR with: the
// manifest's two declared arrays, plus every entry trackedEntries names that
// is not already in m.Entries, appended as a commented-out line inside
// entries (concept.md "The profile manifest > Creating it"). Uncommenting a
// candidate and saving is how an entry gets declared — no lookup, no typing
// its name by hand.
//
// The comments are scaffolding for the buffer only. They are ordinary TOML
// comments, so Decode already ignores them, and Write re-serializes the
// sorted arrays from scratch — nothing here is ever persisted to disk.
func EditBuffer(m Manifest, trackedEntries []string) []byte {
	var candidates []string
	for _, name := range trackedEntries {
		if !m.HasEntry(name) {
			candidates = append(candidates, name)
		}
	}
	sort.Strings(candidates)

	profiles := append([]string{}, m.Profiles...)
	sort.Strings(profiles)
	entries := append([]string{}, m.Entries...)
	sort.Strings(entries)

	var b strings.Builder
	fmt.Fprintf(&b, "profiles = %s\n", quotedInline(profiles))
	b.WriteString("entries = [\n")
	for _, name := range entries {
		fmt.Fprintf(&b, "  %q,\n", name)
	}
	for _, name := range candidates {
		fmt.Fprintf(&b, "  # %q,\n", name)
	}
	b.WriteString("]\n")
	return []byte(b.String())
}

// quotedInline renders names as a single-line TOML array, e.g. ["dark", "light"].
func quotedInline(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = fmt.Sprintf("%q", name)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
