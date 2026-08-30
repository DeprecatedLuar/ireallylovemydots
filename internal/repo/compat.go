package repo

// State classifies a repository's compatibility with dots, per concept.md
// "Compatibility". It is returned rather than derived ad hoc so phase 7's
// `repo init`, phase 8's bootstrap, phase 10's self-healing, and the
// listings can all branch on it instead of re-deriving it.
type State int

const (
	// StateEmpty is a fresh repository: no commits, or no top-level
	// folders. Registers silently — the normal way to start authoring.
	StateEmpty State = iota
	// StateNamespaces is a dots repository: at least one root entry holds
	// a .dots manifest.
	StateNamespaces
	// StateIncompatible has root entries, none holding a .dots — not a
	// dots repository. Registering it would produce a catalogue of
	// folders that are not namespaces.
	StateIncompatible
)

// Inspect classifies root entries into a three-valued compatibility state.
// It takes entries rather than a path so it is pure classification with two
// producers: RootEntries (a git tree, for repo add) and DiskEntries (a
// plain readdir, for repo init against a folder that is not a repository
// yet).
func Inspect(entries []RootEntry) State {
	if len(entries) == 0 {
		return StateEmpty
	}
	for _, e := range entries {
		if e.HasDots {
			return StateNamespaces
		}
	}
	return StateIncompatible
}
