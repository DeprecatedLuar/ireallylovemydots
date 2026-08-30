package engine

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/DeprecatedLuar/dotz/internal/link"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/paths"
	"github.com/DeprecatedLuar/dotz/internal/state"
)

const writeProbePattern = ".dots-write-test-*"

// ProblemKind classifies a single pre-flight finding, per concept.md
// "Enable".
type ProblemKind int

const (
	// ProtectedRoot: the destination is ~, /, or an XDG root itself.
	ProtectedRoot ProblemKind = iota
	// LinkGuard: the destination would resolve inside the data directory,
	// directly or through another entry's link.
	LinkGuard
	// Collision: the destination is already claimed by another enabled
	// namespace.
	Collision
	// Occupied: the destination holds a real file or a non-empty directory.
	Occupied
	// Unwritable: the destination's parent, or its nearest existing
	// ancestor, cannot be written to.
	Unwritable
)

// Problem is one pre-flight finding against a single manifest entry.
type Problem struct {
	Kind  ProblemKind
	Entry manifest.Entry
	// Conflicting is set only for Collision: the enabled namespace already
	// holding the destination.
	Conflicting *state.Key
	Message     string
}

// Preflight collects every pre-flight problem for enabling a namespace's
// entries, per concept.md "Pre-flight": every check runs before any link is
// created, and every result is collected and returned together rather than
// interrogating the caller problem by problem. It touches only the
// destination side of the filesystem, so it can run before the namespace
// itself has been materialized.
func Preflight(key state.Key, namespaceDir string, entries []manifest.Entry, s state.State) ([]Problem, error) {
	idx := BuildIndex(s)
	guarded := selfContainmentGuard(entries)

	var problems []Problem
	for _, e := range entries {
		protected, err := paths.IsProtectedRoot(e.Dest)
		if err != nil {
			return nil, err
		}
		if protected {
			problems = append(problems, Problem{Kind: ProtectedRoot, Entry: e,
				Message: fmt.Sprintf("%s: refusing a protected root (~, /, or an XDG root) as a destination", e.Dest)})
			continue
		}

		if guarded[e.Dest] {
			problems = append(problems, Problem{Kind: LinkGuard, Entry: e,
				Message: fmt.Sprintf("%s: in-repo link guard — another entry in this namespace claims a destination that contains or is contained by this one", e.Dest)})
			continue
		}
		inside, err := paths.InsideDataDir(filepath.Dir(e.Dest))
		if err != nil {
			return nil, err
		}
		if inside {
			problems = append(problems, Problem{Kind: LinkGuard, Entry: e,
				Message: fmt.Sprintf("%s: in-repo link guard — its parent resolves inside the data directory", e.Dest)})
			continue
		}

		if otherKey, ok := idx.Conflict(e.Dest); ok && otherKey != key {
			k := otherKey
			problems = append(problems, Problem{Kind: Collision, Entry: e, Conflicting: &k,
				Message: fmt.Sprintf("%s is already claimed by namespace %q in repository %q", e.Dest, otherKey.Namespace, otherKey.Repo)})
			continue
		}

		payload := filepath.Join(namespaceDir, e.Name)
		occupied, detail, err := occupancy(e.Dest, payload)
		if err != nil {
			return nil, err
		}
		if occupied {
			problems = append(problems, Problem{Kind: Occupied, Entry: e,
				Message: fmt.Sprintf("%s already exists (%s)\n  --force        trash it and link the whole directory\n  or track the paths inside it instead of the parent", e.Dest, detail)})
		}

		writable, err := ancestorWritable(e.Dest)
		if err != nil {
			return nil, err
		}
		if !writable {
			problems = append(problems, Problem{Kind: Unwritable, Entry: e,
				Message: fmt.Sprintf("%s: permission denied", e.Dest)})
		}
	}
	return problems, nil
}

// selfContainmentGuard finds destinations within one namespace's own
// entries that contain, or fall beneath, another entry's destination — the
// in-repo link guard's malformed-manifest case. It is catchable from the
// manifest alone, before any link exists: a namespace holding both "nvim" at
// "~/.config/nvim" and an entry at "~/.config/nvim/init.lua" would, after
// the first link is created, resolve the second destination's parent
// through it, back into the data directory.
func selfContainmentGuard(entries []manifest.Entry) map[string]bool {
	guarded := map[string]bool{}
	for i, a := range entries {
		for j, b := range entries {
			if i == j {
				continue
			}
			if contains(a.Dest, b.Dest) {
				guarded[a.Dest] = true
				guarded[b.Dest] = true
			}
		}
	}
	return guarded
}

// occupancy is concept.md "Occupied destinations"'s general test: does
// anything dots would place at dest collide with what is already there? An
// absent destination, an empty directory, and a dangling symlink are
// absorbed silently and are not occupied; anything else is.
func occupancy(dest, wantTarget string) (occupied bool, detail string, err error) {
	st, err := link.Classify(dest, wantTarget)
	if err != nil {
		return false, "", err
	}
	switch st {
	case link.Missing:
		return false, "", nil
	case link.CorrectSymlink, link.WrongSymlink:
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

// ancestorWritable reports whether dest's parent — or its nearest existing
// ancestor, when the parent does not exist yet — can be written to, per
// concept.md's open question on privileged destinations: a destination dots
// cannot write is a permission error raised in pre-flight, never escalated.
func ancestorWritable(dest string) (bool, error) {
	dir := filepath.Dir(dest)
	for {
		info, err := os.Stat(dir)
		if err == nil {
			if !info.IsDir() {
				return false, nil
			}
			break
		}
		if !os.IsNotExist(err) {
			return false, fmt.Errorf("stat %s: %w", dir, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	probe, err := os.CreateTemp(dir, writeProbePattern)
	if err != nil {
		return false, nil
	}
	name := probe.Name()
	probe.Close()
	os.Remove(name)
	return true, nil
}
