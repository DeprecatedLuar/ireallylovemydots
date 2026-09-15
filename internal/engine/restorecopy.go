package engine

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/DeprecatedLuar/dotz/internal/fscopy"
	"github.com/DeprecatedLuar/dotz/internal/link"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/profile"
	"github.com/DeprecatedLuar/dotz/internal/state"
	"github.com/DeprecatedLuar/dotz/internal/trash"
)

// restoreCopyOp records what a single entry's RestoreCopy actually did, so a
// later failure in the same batch can be unwound precisely, mirroring
// restoreOp in restore.go.
type restoreCopyOp struct {
	dest            string
	copied          bool
	removedLink     bool
	linkTarget      string
	removedEmptyDir bool
	trashedOccupant string
}

// RestoreCopy writes a real copy of every entry's payload to its
// destination, replacing this namespace's own symlink there. Payloads and
// the manifest are untouched — distinct from Restore (`rm --restore`), which
// moves the payload out and stops tracking it. It is transactional across
// the whole set: a failure partway through rolls back every entry already
// copied, and machine state is written only once every entry has succeeded.
//
// Occupancy comes from RestorePreflight, the same pre-flight `rm --restore`
// uses — problems names which destinations are occupied, exactly as
// RestorePreflight left them. skip applies only to those occupied entries,
// per concept.md's "[s] skip this entry, leave the occupant alone": an
// occupied entry is left entirely untouched — not trashed, not copied over —
// while every other entry restores normally regardless of skip. Without
// skip, an occupied destination is trashed first and then replaced by the
// entry's copy.
func RestoreCopy(key state.Key, namespaceDir string, entries []manifest.Entry, problems []RestoreProblem, s state.State, skip bool) error {
	occupied := map[string]bool{}
	for _, p := range problems {
		occupied[p.Entry.Dest] = true
	}

	var completed []restoreCopyOp
	rollback := func() {
		for i := len(completed) - 1; i >= 0; i-- {
			op := completed[i]
			if op.copied {
				os.RemoveAll(op.dest)
			}
			switch {
			case op.trashedOccupant != "":
				trash.Restore(op.trashedOccupant, op.dest)
			case op.removedLink:
				link.Create(op.dest, op.linkTarget)
			case op.removedEmptyDir:
				os.MkdirAll(op.dest, dirPerm)
			}
		}
	}

	activeProfile := s.Entries[key].ActiveProfile
	for _, e := range entries {
		if !e.HasDestination() {
			// Nothing is ever linked at an empty or manifest.DestNone
			// destination, so there is nothing to replace.
			continue
		}

		if occupied[e.Dest] && skip {
			// Left entirely alone: not trashed, not copied over.
			continue
		}

		op := restoreCopyOp{dest: e.Dest}

		if occupied[e.Dest] {
			name, err := trash.Move(e.Dest)
			if err != nil {
				rollback()
				return fmt.Errorf("trash occupied destination %s: %w", e.Dest, err)
			}
			op.trashedOccupant = name
		} else {
			rootPayload := filepath.Join(namespaceDir, e.Name)
			st, err := link.Classify(e.Dest, rootPayload)
			if err != nil {
				rollback()
				return err
			}
			switch st {
			case link.CorrectSymlink, link.WrongSymlink:
				target, readErr := link.Read(e.Dest)
				if readErr != nil {
					rollback()
					return readErr
				}
				if err := link.Remove(e.Dest); err != nil {
					rollback()
					return err
				}
				op.removedLink = true
				op.linkTarget = target
			case link.RealDir:
				if err := os.Remove(e.Dest); err != nil {
					rollback()
					return fmt.Errorf("remove empty directory %s: %w", e.Dest, err)
				}
				op.removedEmptyDir = true
			}
		}

		// A profiled entry's real content lives wherever its active profile
		// currently resolves it to — the same source enable links from —
		// never the namespace root copy, which may be a different version.
		payload, err := profile.Source(namespaceDir, e.Name, activeProfile)
		if err != nil {
			rollback()
			return err
		}
		if err := os.MkdirAll(filepath.Dir(e.Dest), dirPerm); err != nil {
			rollback()
			return fmt.Errorf("create parent directory for %s: %w", e.Dest, err)
		}
		if err := fscopy.Copy(payload, e.Dest); err != nil {
			rollback()
			return fmt.Errorf("copy %s to %s: %w", payload, e.Dest, err)
		}
		op.copied = true
		completed = append(completed, op)
	}

	entry := s.Entries[key]
	entry.Enabled = false
	entry.LinkedDests = nil
	s.Entries[key] = entry
	if err := state.Write(s); err != nil {
		rollback()
		return err
	}

	return nil
}
