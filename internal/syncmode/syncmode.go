// Package syncmode decides, per top-level repository entry, which version
// sync commits and which it leaves in the working tree. It holds no I/O.
package syncmode

import (
	"errors"
	"fmt"
	"strings"

	"github.com/DeprecatedLuar/ireallylovemydots/internal/git"
)

// Mode is how one namespace syncs.
type Mode string

const (
	Merge           Mode = "merge"
	OverwriteRemote Mode = "overwrite-remote"
	OverwriteLocal  Mode = "overwrite-local"
	Overlay         Mode = "overlay"
)

// Modes lists every mode, in the order help and errors name them.
var Modes = []Mode{Merge, OverwriteRemote, OverwriteLocal, Overlay}

// ErrReadOnlyOverwriteRemote is returned for overwrite-remote on a
// repository this machine cannot push to.
var ErrReadOnlyOverwriteRemote = errors.New("overwrite-remote needs push access, and this repository is read-only on this machine")

// ErrRootConflict is returned for a conflicted entry that is not a
// namespace: it has nowhere to be held.
var ErrRootConflict = errors.New("conflicts with the remote outside any namespace")

// Parse reads a mode name.
func Parse(s string) (Mode, error) {
	for _, m := range Modes {
		if string(m) == s {
			return m, nil
		}
	}
	names := make([]string, len(Modes))
	for i, m := range Modes {
		names[i] = string(m)
	}
	return "", fmt.Errorf("unknown sync mode %q; one of: %s", s, strings.Join(names, ", "))
}

// Default is the mode of a namespace with none saved.
func Default(readOnly bool) Mode {
	if readOnly {
		return Overlay
	}
	return Merge
}

// Effective resolves a namespace's mode for one run: flag beats saved,
// saved beats the default. A read-only repository never commits, so merge
// runs as overlay there and overwrite-remote is refused.
func Effective(flag, saved Mode, readOnly bool) (Mode, error) {
	m := Default(readOnly)
	if saved != "" {
		m = saved
	}
	if flag != "" {
		m = flag
	}
	if readOnly {
		switch m {
		case OverwriteRemote:
			return "", ErrReadOnlyOverwriteRemote
		case Merge:
			return Overlay, nil
		}
	}
	return m, nil
}

// Unit is what Decide needs to know about one top-level entry.
type Unit struct {
	// Dir is true for a namespace, false for a loose root file.
	Dir bool
	// InScope is false for a namespace this run was not asked to sync.
	InScope      bool
	ChangedLocal bool
	Conflicted   bool
	Mode         Mode
}

// Outcome is Decide's answer for one entry.
type Outcome struct {
	Placement git.Placement
	// Held means the entry keeps its local files and its saved held base.
	Held bool
	// Trash means the entry's local edits go to trash before applying.
	Trash bool
}

var (
	takeRemote = git.Placement{Commit: git.SourceRemote, Worktree: git.SourceRemote}
	holdLocal  = git.Placement{Commit: git.SourceRemote, Worktree: git.SourceUntouched}
)

// Decide places one entry. An entry with no local changes always takes the
// remote; one with local changes follows its mode, and is held when it is
// out of scope or conflicts in merge or overlay.
func Decide(u Unit) (Outcome, error) {
	if !u.ChangedLocal {
		return Outcome{Placement: takeRemote}, nil
	}
	if !u.InScope {
		return Outcome{Placement: holdLocal, Held: true}, nil
	}
	switch u.Mode {
	case OverwriteRemote:
		return Outcome{Placement: git.Placement{Commit: git.SourceLocal, Worktree: git.SourceUntouched}}, nil
	case OverwriteLocal:
		return Outcome{Placement: takeRemote, Trash: true}, nil
	}
	if u.Conflicted {
		if !u.Dir {
			return Outcome{}, ErrRootConflict
		}
		return Outcome{Placement: holdLocal, Held: true}, nil
	}
	if u.Mode == Overlay {
		return Outcome{Placement: git.Placement{Commit: git.SourceRemote, Worktree: git.SourceMerged}}, nil
	}
	return Outcome{Placement: git.Placement{Commit: git.SourceMerged, Worktree: git.SourceMerged}}, nil
}
