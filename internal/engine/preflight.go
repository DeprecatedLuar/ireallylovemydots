package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeprecatedLuar/ireallylovemydots/internal/link"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/manifest"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/paths"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/state"
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
	// NamespaceCollision: the destination is held by another enabled
	// namespace, either by claiming it or by linking one of its parents.
	NamespaceCollision
	// RealFileCollision: the destination, or a parent link, holds something
	// that is not an enabled namespace: a real file, a non-empty directory,
	// or a link into the data directory naming no enabled namespace.
	RealFileCollision
	// Unwritable: the destination's parent, or its nearest existing
	// ancestor, cannot be written to.
	Unwritable
)

// Problem is one pre-flight finding against a single manifest entry.
type Problem struct {
	Kind  ProblemKind
	Entry manifest.Entry
	// Conflicting is set only for NamespaceCollision: the enabled namespace
	// already holding the destination.
	Conflicting *state.Key
	// Path is the path actually in the way: Entry.Dest for every problem
	// except a parent-link RealFileCollision, where it is the link.
	Path string
	// Detail names what is in the way beyond its path; empty for a real
	// file or directory, whose path alone says what it is.
	Detail  string
	Message string
}

// dataLinkKeyComponents is how many leading components of a data-directory
// link's target name a namespace: "<repo>/<namespace>".
const dataLinkKeyComponents = 2

// Preflight collects every pre-flight problem for enabling a namespace's
// entries, per concept.md "Pre-flight": every check runs before any link is
// created, and every result is collected and returned together rather than
// interrogating the caller problem by problem. It touches only the
// destination side of the filesystem, so it can run before the namespace
// itself has been materialized.
func Preflight(key state.Key, namespaceDir string, entries []manifest.Entry, s state.State) ([]Problem, error) {
	idx := BuildIndex(s)
	guarded := manifestGuardProblems(entries)

	var problems []Problem
	for _, e := range entries {
		if !e.HasDestination() {
			// An empty destination or manifest.DestNone names nothing to
			// link — neither is this pass's problem to raise; an empty one
			// is a listing/repair concern (concept.md "Manual edits") and
			// DestNone is not a problem at all.
			continue
		}
		protected, err := paths.IsProtectedRoot(e.Dest)
		if err != nil {
			return nil, err
		}
		if protected {
			problems = append(problems, Problem{Kind: ProtectedRoot, Entry: e, Path: e.Dest,
				Message: fmt.Sprintf("%s: refusing a protected root (~, /, or an XDG root) as a destination", e.Dest)})
			continue
		}

		if detail, ok := guarded[e.Name]; ok {
			problems = append(problems, Problem{Kind: LinkGuard, Entry: e, Path: e.Dest,
				Message: fmt.Sprintf("%s: in-repo link guard, %s", e.Dest, detail)})
			continue
		}
		if otherKey, ok := idx.Conflict(e.Dest); ok && otherKey != key {
			k := otherKey
			problems = append(problems, Problem{Kind: NamespaceCollision, Entry: e, Conflicting: &k, Path: e.Dest,
				Message: fmt.Sprintf("%s is already claimed by namespace %q in repository %q", e.Dest, otherKey.Namespace, otherKey.Repo)})
			continue
		}

		parentProblems, err := parentLinkProblems(e, key, s)
		if err != nil {
			return nil, err
		}
		if len(parentProblems) > 0 {
			problems = append(problems, parentProblems...)
			continue
		}

		inside, err := paths.InsideDataDir(filepath.Dir(e.Dest))
		if err != nil {
			return nil, err
		}
		if inside {
			problems = append(problems, Problem{Kind: LinkGuard, Entry: e, Path: e.Dest,
				Message: fmt.Sprintf("%s: in-repo link guard, its parent resolves inside the data directory", e.Dest)})
			continue
		}

		payload := filepath.Join(namespaceDir, e.Name)
		occupied, err := occupancy(e.Dest, payload)
		if err != nil {
			return nil, err
		}
		if occupied {
			problems = append(problems, Problem{Kind: RealFileCollision, Entry: e, Path: e.Dest,
				Message: fmt.Sprintf("%s already exists\n  --force        trash it and link the whole directory\n  or track the paths inside it instead of the parent", manifest.DisplayPath(e.Dest))})
		}

		writable, err := ancestorWritable(e.Dest)
		if err != nil {
			return nil, err
		}
		if !writable {
			problems = append(problems, Problem{Kind: Unwritable, Entry: e, Path: e.Dest,
				Message: fmt.Sprintf("%s: permission denied", e.Dest)})
		}
	}
	return problems, nil
}

// parentLinkProblems walks the ancestors of e.Dest for symlinks into the data
// directory, per concept.md "Conflicts": a link naming an enabled namespace
// other than key is a NamespaceCollision with that namespace; any other link
// (a disabled namespace, the repository root, key itself) is a
// RealFileCollision at the link, which --force removes without touching what
// it points at.
func parentLinkProblems(e manifest.Entry, key state.Key, s state.State) ([]Problem, error) {
	links, err := paths.DataDirLinks(e.Dest)
	if err != nil {
		return nil, err
	}
	var problems []Problem
	seen := map[state.Key]bool{}
	for _, l := range links {
		parts := strings.Split(filepath.ToSlash(l.Rel), "/")
		if len(parts) >= dataLinkKeyComponents {
			other := state.Key{Repo: parts[0], Namespace: parts[1]}
			if other != key && s.Entries[other].Enabled {
				if seen[other] {
					continue
				}
				seen[other] = true
				k := other
				problems = append(problems, Problem{Kind: NamespaceCollision, Entry: e, Conflicting: &k, Path: e.Dest,
					Message: fmt.Sprintf("%s is blocked by namespace %q in repository %q", e.Dest, other.Namespace, other.Repo)})
				continue
			}
		}
		target, readErr := link.Read(l.Path)
		if readErr != nil {
			return nil, readErr
		}
		detail := "link to " + manifest.DisplayPath(target)
		problems = append(problems, Problem{Kind: RealFileCollision, Entry: e, Path: l.Path, Detail: detail,
			Message: fmt.Sprintf("%s already exists (%s)", l.Path, detail)})
	}
	return problems, nil
}

// manifestGuardProblems runs manifest.Validate over entries and keys its
// findings by entry name, so Preflight's own per-entry loop can look one up
// in O(1) — the in-repo link guard's malformed-manifest case (concept.md
// "The in-repo link guard"): two entries naming the same destination, or one
// destination falling inside another, both catchable from the manifest
// alone, before any link exists. Sharing manifest.Validate here rather than
// re-deriving the same check means Preflight, the listing, and `namespace
// <ns> edit` can never disagree about what counts as guarded.
func manifestGuardProblems(entries []manifest.Entry) map[string]string {
	guarded := map[string]string{}
	for _, p := range manifest.Validate(manifest.Manifest{Entries: entries}) {
		guarded[p.Entry] = p.Detail
	}
	return guarded
}

// occupancy is concept.md "Occupied destinations"'s general test: does
// anything dots would place at dest collide with what is already there? An
// absent destination, an empty directory, and a symlink — dangling or
// pointing somewhere live — are absorbed silently and are not occupied; a
// symlink holds no data of its own, so replacing one loses nothing. Anything
// else is.
func occupancy(dest, wantTarget string) (bool, error) {
	st, err := link.Classify(dest, wantTarget)
	if err != nil {
		return false, err
	}
	switch st {
	case link.Missing, link.CorrectSymlink, link.WrongSymlink:
		return false, nil
	case link.RealDir:
		dirEntries, readErr := os.ReadDir(dest)
		if readErr != nil {
			return false, fmt.Errorf("read directory %s: %w", dest, readErr)
		}
		return len(dirEntries) > 0, nil
	default: // link.RealFile
		return true, nil
	}
}

// Occupancy is occupancy's exported form, for a caller outside pre-flight
// that needs the same occupied/absorbable test against an arbitrary
// destination — namely classifyEntry (internal/commands/listing.go), so a
// disabled namespace's "!" row in `dots <ns>` can name the same destination
// pre-flight would report if the namespace were re-enabled, per
// concept.md "What enable reports": "a `!` entry there carries the
// destination."
func Occupancy(dest, wantTarget string) (bool, error) {
	return occupancy(dest, wantTarget)
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
