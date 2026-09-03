package engine

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/DeprecatedLuar/dotz/internal/link"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/paths"
	"github.com/DeprecatedLuar/dotz/internal/profile"
)

// LinkFailure is one destination converge could not bring into place: a real
// file or directory occupies it, or it resolves inside dots' own data
// directory. Never destroyed — reported and left alone.
type LinkFailure struct {
	Entry  manifest.Entry
	Dest   string
	Detail string
}

// restoredLink records one destination converge repointed, with the target
// it held before, so a transactional failure partway through can put every
// earlier destination back exactly as found.
type restoredLink struct {
	dest string
	// target is empty when the destination held no symlink at all, in which
	// case rolling back means removing the one converge created.
	target string
}

// converge is the single mechanism behind every place dots repoints a
// namespace's links onto what its manifest and active profile currently
// imply: a profile switch (the wanted target changes), or a repository/
// namespace rename and self-heal (the destination's current target has gone
// stale). It never creates or removes anything at a destination that is not
// already a symlink — a real file or directory is always reported, never
// destroyed.
//
// It returns two lists for two different callers: linked names every
// destination that ends up correctly linked, whether converge had to touch
// it or it was already correct — what a caller persisting state.LinkedDests
// needs. repointed is the narrower subset converge actually created or
// repointed this call — what a caller reporting "relinked X" to the user
// needs, since re-announcing a destination that never changed would be
// noise.
//
// transactional selects the failure policy, which is not a caller
// preference but a consequence of whether the links were valid when
// converge started:
//
//   - true (SwitchProfile): the namespace was fully linked before the call,
//     so a failure partway through rolls back every destination already
//     repointed and returns the error — half-applying a profile switch is
//     worse than not switching at all.
//   - false (Relink): the caller is trying to repair links that are already
//     wrong (a rename repointed the namespace's own directory, or self-heal
//     found drift), so there is nothing correct to preserve by rolling
//     back — rolling back would only recreate the very links converge was
//     asked to fix. Every destination converge can bring into place is
//     linked; every one it can't is collected as a LinkFailure and reported
//     rather than aborting the rest.
func converge(namespaceDir string, entries []manifest.Entry, activeProfile string, transactional bool) (linked []string, repointed []string, failures []LinkFailure, err error) {
	var undo []restoredLink
	rollback := func() {
		for i := len(undo) - 1; i >= 0; i-- {
			link.Remove(undo[i].dest)
			if undo[i].target != "" {
				link.Create(undo[i].dest, undo[i].target)
			}
		}
	}

	for _, e := range entries {
		if !e.HasDestination() {
			continue
		}

		// A destination whose parent resolves inside dots' own data
		// directory is never reachable through `namespace add` (guarded at
		// track time), but a manifest can still carry one — hand-edited,
		// pulled from another machine, or rebuilt by self-heal's manifest
		// recovery. Checked on the parent, not e.Dest itself: a correctly
		// linked entry's Dest is a symlink whose target already lives inside
		// the data directory by design, so resolving e.Dest itself would
		// flag every healthy link.
		inside, insideErr := paths.InsideDataDir(filepath.Dir(e.Dest))
		if insideErr != nil {
			if transactional {
				rollback()
			}
			return nil, nil, nil, insideErr
		}
		if inside {
			failures = append(failures, LinkFailure{Entry: e, Dest: e.Dest, Detail: "destination resolves inside dots' own data directory"})
			continue
		}

		want, sourceErr := profile.Source(namespaceDir, e.Name, activeProfile)
		if sourceErr != nil {
			if transactional {
				rollback()
			}
			return nil, nil, nil, sourceErr
		}

		st, classifyErr := link.Classify(e.Dest, want)
		if classifyErr != nil {
			if transactional {
				rollback()
				return nil, nil, nil, classifyErr
			}
			failures = append(failures, LinkFailure{Entry: e, Dest: e.Dest, Detail: classifyErr.Error()})
			continue
		}

		switch st {
		case link.CorrectSymlink:
			linked = append(linked, e.Dest)
			continue
		case link.RealFile, link.RealDir:
			failures = append(failures, LinkFailure{Entry: e, Dest: e.Dest, Detail: "real file or directory occupies the destination"})
			if transactional {
				rollback()
				return nil, nil, nil, fmt.Errorf("%s: a real file or directory occupies the destination", e.Dest)
			}
			continue
		}

		previous := ""
		if st == link.WrongSymlink {
			read, readErr := link.Read(e.Dest)
			if readErr != nil {
				if transactional {
					rollback()
					return nil, nil, nil, readErr
				}
				failures = append(failures, LinkFailure{Entry: e, Dest: e.Dest, Detail: readErr.Error()})
				continue
			}
			previous = read
			if removeErr := link.Remove(e.Dest); removeErr != nil {
				if transactional {
					rollback()
					return nil, nil, nil, removeErr
				}
				failures = append(failures, LinkFailure{Entry: e, Dest: e.Dest, Detail: removeErr.Error()})
				continue
			}
		}
		if mkdirErr := os.MkdirAll(filepath.Dir(e.Dest), dirPerm); mkdirErr != nil {
			if transactional {
				rollback()
				return nil, nil, nil, fmt.Errorf("create parent directory for %s: %w", e.Dest, mkdirErr)
			}
			failures = append(failures, LinkFailure{Entry: e, Dest: e.Dest, Detail: mkdirErr.Error()})
			continue
		}
		if createErr := link.Create(e.Dest, want); createErr != nil {
			if transactional {
				rollback()
				return nil, nil, nil, createErr
			}
			failures = append(failures, LinkFailure{Entry: e, Dest: e.Dest, Detail: createErr.Error()})
			continue
		}
		undo = append(undo, restoredLink{dest: e.Dest, target: previous})
		linked = append(linked, e.Dest)
		repointed = append(repointed, e.Dest)
	}

	return linked, repointed, failures, nil
}

// Relink repoints every entry's destination onto its current manifest and
// active-profile target, best-effort: used wherever the namespace's own
// links have already gone stale out from under it — a repository or
// namespace rename (the directory the target points into moved), and
// self-heal's reconciliation pass. Never rolls back a partial result, since
// there is nothing correct to preserve by undoing it — see converge's doc
// comment. The returned list is every destination now correctly linked
// (state.LinkedDests' contract), not just the ones converge had to touch.
func Relink(namespaceDir string, entries []manifest.Entry, activeProfile string) ([]string, []LinkFailure, error) {
	linked, _, failures, err := converge(namespaceDir, entries, activeProfile, false)
	return linked, failures, err
}
