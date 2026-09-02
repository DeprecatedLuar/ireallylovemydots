package engine

import (
	"os"

	"github.com/DeprecatedLuar/dotz/internal/link"
	"github.com/DeprecatedLuar/dotz/internal/state"
)

// Disable removes every symlink recorded for a namespace's state entry,
// flips it to disabled, and clears the recorded links. Per concept.md
// "disable is not destructive": files stay exactly where they are inside
// the namespace, and re-enabling is instant. A destination that no longer
// holds the recorded symlink (drift) is left alone rather than destroyed —
// drift detection is self-healing's job, not disable's.
func Disable(key state.Key, s state.State) error {
	entry, ok := s.Entries[key]
	if !ok || !entry.Enabled {
		return nil
	}

	for _, dest := range entry.LinkedDests {
		info, err := os.Lstat(dest)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		if err := link.Remove(dest); err != nil {
			return err
		}
	}

	// The active profile is kept: disable removes symlinks, it does not
	// forget which version of an entry belongs at a destination, so
	// re-enabling puts back exactly what was linked before.
	s.Entries[key] = state.Entry{Enabled: false, ActiveProfile: entry.ActiveProfile}
	return state.Write(s)
}
