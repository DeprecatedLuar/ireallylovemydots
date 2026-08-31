package engine

import (
	"fmt"
	"path/filepath"

	"github.com/DeprecatedLuar/dotz/internal/link"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/state"
	"github.com/DeprecatedLuar/dotz/internal/trash"
)

// trashOp records what trashing a single entry actually did, so a later
// failure in the same batch can be unwound precisely — the same shape as
// restoreOp in restore.go.
type trashOp struct {
	dest        string
	payload     string
	removedLink bool
	trashedName string
}

// TrashEntries implements rm's default removal path, per concept.md
// "Removal": every entry's payload goes to the XDG trash and nothing is
// written to the home directory. Callers are responsible for having
// disabled the namespace first (rm always does, unconditionally) — this
// function itself only clears a destination symlink when it is exactly the
// entry's own correct link, the same case Restore's purge path already
// handles: `namespace <ns> add` symlinks a destination immediately at track
// time, independent of enable/disable state, so a never-enabled entry can
// still have a live symlink standing at its destination.
//
// Transactional across the whole set: a failure partway through restores
// every already-trashed payload (and any symlink removed to get there) back
// into the namespace, and neither the manifest nor machine state is written
// until every entry has succeeded. Returns the trash name for every entry
// trashed, so a caller acting under --purge can erase them once the whole
// removal has succeeded.
func TrashEntries(key state.Key, namespaceDir string, targets []manifest.Entry, s state.State) ([]string, error) {
	var completed []trashOp
	rollback := func() {
		for i := len(completed) - 1; i >= 0; i-- {
			op := completed[i]
			trash.Restore(op.trashedName, op.payload)
			if op.removedLink {
				link.Create(op.dest, op.payload)
			}
		}
	}

	for _, e := range targets {
		payload := filepath.Join(namespaceDir, e.Name)
		op := trashOp{dest: e.Dest, payload: payload}

		if st, err := link.Classify(e.Dest, payload); err == nil && st == link.CorrectSymlink {
			if err := link.Remove(e.Dest); err != nil {
				rollback()
				return nil, err
			}
			op.removedLink = true
		}

		name, err := trash.Move(payload)
		if err != nil {
			rollback()
			return nil, fmt.Errorf("trash %s: %w", payload, err)
		}
		op.trashedName = name
		completed = append(completed, op)
	}

	m, err := manifest.Read(namespaceDir)
	if err != nil {
		rollback()
		return nil, err
	}
	removedNames := make(map[string]bool, len(targets))
	removedDests := make(map[string]bool, len(targets))
	for _, e := range targets {
		removedNames[e.Name] = true
		removedDests[e.Dest] = true
	}
	var remaining []manifest.Entry
	for _, e := range m.Entries {
		if !removedNames[e.Name] {
			remaining = append(remaining, e)
		}
	}
	if err := manifest.Write(namespaceDir, manifest.Manifest{Entries: remaining}); err != nil {
		rollback()
		return nil, err
	}

	if entry, ok := s.Entries[key]; ok {
		var remainingDests []string
		for _, d := range entry.LinkedDests {
			if !removedDests[d] {
				remainingDests = append(remainingDests, d)
			}
		}
		entry.LinkedDests = remainingDests
		s.Entries[key] = entry
		if err := state.Write(s); err != nil {
			rollback()
			return nil, err
		}
	}

	trashed := make([]string, len(completed))
	for i, op := range completed {
		trashed[i] = op.trashedName
	}
	return trashed, nil
}
