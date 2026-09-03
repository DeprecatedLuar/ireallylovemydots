package engine

import (
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/profile"
	"github.com/DeprecatedLuar/dotz/internal/state"
)

// SwitchProfile makes newProfile — empty or profile.Main for the unprofiled
// layer — the namespace's active profile, relinking exactly the entries
// whose link target changes and leaving every other destination alone, per
// concept.md "Switching". Root files are never modified.
//
// It is transactional: a failure partway through restores every destination
// it had already repointed and writes no state. State is written only once
// every relink has succeeded. See converge's doc comment for why this is the
// transactional door onto the shared relinking mechanism, rather than
// Relink's best-effort one.
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

	_, repointed, _, err := converge(namespaceDir, entries, newProfile, true)
	if err != nil {
		return nil, err
	}

	entry.ActiveProfile = newProfile
	s.Entries[key] = entry
	if err := state.Write(s); err != nil {
		return nil, err
	}
	return repointed, nil
}
