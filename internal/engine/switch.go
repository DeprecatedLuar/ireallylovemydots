package engine

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/DeprecatedLuar/dotz/internal/link"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/profile"
	"github.com/DeprecatedLuar/dotz/internal/state"
)

// restoredLink records one destination SwitchProfile repointed, with the
// target it held before, so a failure partway through can put every earlier
// destination back exactly as found.
type restoredLink struct {
	dest string
	// target is empty when the destination held no symlink at all, in which
	// case rolling back means removing the one SwitchProfile created.
	target string
}

// SwitchProfile makes newProfile — empty or profile.Main for the unprofiled
// layer — the namespace's active profile, relinking exactly the entries
// whose link target changes and leaving every other destination alone, per
// concept.md "Switching". Root files are never modified.
//
// It is transactional like Enable: a failure partway through restores every
// destination it had already repointed and writes no state. State is written
// only once every relink has succeeded.
//
// A disabled namespace has no symlinks to move, so the switch is recorded in
// state and nothing on the filesystem is touched — enabling it later links
// the new profile's version straight away.
func SwitchProfile(key state.Key, namespaceDir string, entries []manifest.Entry, s state.State, newProfile string) ([]string, error) {
	if newProfile == profile.Main {
		newProfile = ""
	}

	entry := s.Entries[key]
	if !entry.Enabled {
		entry.ActiveProfile = newProfile
		s.Entries[key] = entry
		return nil, state.Write(s)
	}

	var relinked []string
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
		want, err := profile.Source(namespaceDir, e.Name, newProfile)
		if err != nil {
			rollback()
			return nil, err
		}

		st, err := link.Classify(e.Dest, want)
		if err != nil {
			rollback()
			return nil, err
		}
		switch st {
		case link.CorrectSymlink:
			continue
		case link.RealFile, link.RealDir:
			rollback()
			return nil, fmt.Errorf("%s: a real file or directory occupies the destination", e.Dest)
		}

		previous := ""
		if st == link.WrongSymlink {
			previous, err = link.Read(e.Dest)
			if err != nil {
				rollback()
				return nil, err
			}
			if err := link.Remove(e.Dest); err != nil {
				rollback()
				return nil, err
			}
		}
		if err := os.MkdirAll(filepath.Dir(e.Dest), dirPerm); err != nil {
			rollback()
			return nil, fmt.Errorf("create parent directory for %s: %w", e.Dest, err)
		}
		if err := link.Create(e.Dest, want); err != nil {
			rollback()
			return nil, err
		}
		undo = append(undo, restoredLink{dest: e.Dest, target: previous})
		relinked = append(relinked, e.Dest)
	}

	entry.ActiveProfile = newProfile
	s.Entries[key] = entry
	if err := state.Write(s); err != nil {
		rollback()
		return nil, err
	}
	return relinked, nil
}
