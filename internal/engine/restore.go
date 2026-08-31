package engine

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/DeprecatedLuar/dotz/internal/link"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/state"
	"github.com/DeprecatedLuar/dotz/internal/trash"
)

// RestoreProblem is one occupied-destination finding against a single entry
// being removed, per concept.md "Occupied destinations on removal".
type RestoreProblem struct {
	Entry   manifest.Entry
	Message string
}

// RestorePreflight collects every occupied-destination problem for a set of
// entries being removed, per concept.md: every destination is checked and
// every result collected before anything is restored, the same shape as
// enable's pre-flight. A destination currently holding this entry's own
// correct symlink is the ordinary enabled case, not a conflict; absent, an
// empty directory, and a dangling symlink hold no data and are absorbed
// silently, exactly as on the enable side.
func RestorePreflight(namespaceDir, namespaceName string, entries []manifest.Entry) ([]RestoreProblem, error) {
	var problems []RestoreProblem
	for _, e := range entries {
		payload := filepath.Join(namespaceDir, e.Name)
		occupied, detail, err := restoreOccupancy(e.Dest, payload)
		if err != nil {
			return nil, err
		}
		if occupied {
			problems = append(problems, RestoreProblem{
				Entry: e,
				Message: fmt.Sprintf(
					"%s already exists (%s)\n  [t] trash it and restore %s's copy\n  [s] skip this entry, leave the occupant alone\n  [c] cancel",
					e.Dest, detail, namespaceName),
			})
		}
	}
	return problems, nil
}

// restoreOccupancy is concept.md "Occupied destinations"'s general test,
// adapted for removal: a destination already holding this entry's own
// correct symlink is not a conflict, since that is exactly the enabled state
// being unwound. Otherwise the rule is identical to the enable side: absent,
// an empty directory, and a dangling symlink are absorbed silently; anything
// else is occupied.
func restoreOccupancy(dest, payload string) (occupied bool, detail string, err error) {
	st, err := link.Classify(dest, payload)
	if err != nil {
		return false, "", err
	}
	switch st {
	case link.Missing, link.CorrectSymlink:
		return false, "", nil
	case link.WrongSymlink:
		if _, statErr := os.Stat(dest); os.IsNotExist(statErr) {
			return false, "", nil
		}
		return true, "symlink", nil
	case link.RealDir:
		dirEntries, readErr := os.ReadDir(dest)
		if readErr != nil {
			return false, "", fmt.Errorf("read directory %s: %w", dest, readErr)
		}
		if len(dirEntries) == 0 {
			return false, "", nil
		}
		return true, fmt.Sprintf("real directory, %d entries", len(dirEntries)), nil
	default: // link.RealFile
		return true, "real file", nil
	}
}

// restoreOp records what a single entry's restore actually did, so a later
// failure in the same batch can be unwound precisely.
type restoreOp struct {
	name            string
	payload         string
	dest            string
	removedLink     bool
	removedEmptyDir bool
	trashedOccupant string
	purgedPayload   string
}

// Restore restores every entry in targets to its destination as a real
// file — removing any symlink of its own first, moving the payload out of
// the namespace, and removing the manifest entry — or, under skip, trashes
// the payload instead and leaves the destination untouched. It is
// transactional across the whole set: a failure partway through rolls back
// every entry already restored or trashed, and neither the manifest nor
// machine state is written until every entry has succeeded. It returns the
// trash name of everything it sent to the trash — a displaced occupant, or
// a skipped entry's own payload — so a caller running under --purge can
// erase them once the whole restore has verifiably succeeded, per
// concept.md "--restore --purge": "the restore completes and is verified
// before anything is erased."
//
// problems, from RestorePreflight, names which destinations are occupied.
// skip applies only to those occupied entries — concept.md's "[s] skip this
// entry, leave the occupant alone" is scoped to the specific entry the
// prompt was asking about, not the whole batch. An entry whose destination
// is not occupied is always restored normally, regardless of skip. Without
// skip, an occupied destination is trashed first and then replaced by the
// restored payload — concept.md's "--force is [t]" — which by this point
// the caller has already confirmed.
func Restore(key state.Key, namespaceDir string, targets []manifest.Entry, problems []RestoreProblem, s state.State, skip bool) ([]string, error) {
	occupied := map[string]bool{}
	for _, p := range problems {
		occupied[p.Entry.Dest] = true
	}

	var completed []restoreOp
	rollback := func() {
		for i := len(completed) - 1; i >= 0; i-- {
			op := completed[i]
			if op.purgedPayload != "" {
				trash.Restore(op.purgedPayload, op.payload)
				if op.removedLink {
					link.Create(op.dest, op.payload)
				}
				continue
			}
			os.Rename(op.dest, op.payload)
			switch {
			case op.trashedOccupant != "":
				trash.Restore(op.trashedOccupant, op.dest)
			case op.removedLink:
				link.Create(op.dest, op.payload)
			case op.removedEmptyDir:
				os.MkdirAll(op.dest, dirPerm)
			}
		}
	}

	for _, e := range targets {
		payload := filepath.Join(namespaceDir, e.Name)
		op := restoreOp{name: e.Name, payload: payload, dest: e.Dest}

		if skip && occupied[e.Dest] {
			// A tracked entry is symlinked at its destination from the
			// moment it is added, independent of enable/disable state, so
			// skipping must clear that symlink too — otherwise the payload
			// it names is trashed out from under it, leaving a dangling
			// link behind for a namespace nothing restores into.
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
			op.purgedPayload = name
			completed = append(completed, op)
			continue
		}

		if occupied[e.Dest] {
			name, err := trash.Move(e.Dest)
			if err != nil {
				rollback()
				return nil, fmt.Errorf("trash occupied destination %s: %w", e.Dest, err)
			}
			op.trashedOccupant = name
		} else {
			st, err := link.Classify(e.Dest, payload)
			if err != nil {
				rollback()
				return nil, err
			}
			switch st {
			case link.CorrectSymlink, link.WrongSymlink:
				if err := link.Remove(e.Dest); err != nil {
					rollback()
					return nil, err
				}
				op.removedLink = true
			case link.RealDir:
				if err := os.Remove(e.Dest); err != nil {
					rollback()
					return nil, fmt.Errorf("remove empty directory %s: %w", e.Dest, err)
				}
				op.removedEmptyDir = true
			}
		}

		if err := os.MkdirAll(filepath.Dir(e.Dest), dirPerm); err != nil {
			rollback()
			return nil, fmt.Errorf("create parent directory for %s: %w", e.Dest, err)
		}
		if err := os.Rename(payload, e.Dest); err != nil {
			rollback()
			return nil, fmt.Errorf("restore %s: %w", e.Dest, err)
		}
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

	var trashed []string
	for _, op := range completed {
		switch {
		case op.trashedOccupant != "":
			trashed = append(trashed, op.trashedOccupant)
		case op.purgedPayload != "":
			trashed = append(trashed, op.purgedPayload)
		}
	}
	return trashed, nil
}
