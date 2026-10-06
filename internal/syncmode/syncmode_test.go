package syncmode

import (
	"errors"
	"testing"

	"github.com/DeprecatedLuar/ireallylovemydots/internal/git"
)

func TestParse(t *testing.T) {
	for _, m := range Modes {
		got, err := Parse(string(m))
		if err != nil || got != m {
			t.Fatalf("Parse(%q) = %q, %v", m, got, err)
		}
	}
	if _, err := Parse("local"); err == nil {
		t.Fatal("expected an error for an unknown mode")
	}
}

func TestEffective(t *testing.T) {
	cases := []struct {
		name        string
		flag, saved Mode
		readOnly    bool
		want        Mode
		wantErr     error
	}{
		{"default writable", "", "", false, Merge, nil},
		{"default read-only", "", "", true, Overlay, nil},
		{"saved wins", "", Overlay, false, Overlay, nil},
		{"flag beats saved", OverwriteLocal, Overlay, false, OverwriteLocal, nil},
		{"merge on read-only is overlay", Merge, "", true, Overlay, nil},
		{"overwrite-remote on read-only errors", OverwriteRemote, "", true, "", ErrReadOnlyOverwriteRemote},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Effective(tc.flag, tc.saved, tc.readOnly)
			if !errors.Is(err, tc.wantErr) || got != tc.want {
				t.Fatalf("Effective = %q, %v; want %q, %v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestDecide(t *testing.T) {
	remote := git.Placement{Commit: git.SourceRemote, Worktree: git.SourceRemote}
	held := Outcome{Placement: git.Placement{Commit: git.SourceRemote, Worktree: git.SourceUntouched}, Held: true}
	cases := []struct {
		name string
		unit Unit
		want Outcome
	}{
		{"clean takes remote even out of scope", Unit{Dir: true, Mode: Overlay}, Outcome{Placement: remote}},
		{"edited out of scope is held", Unit{Dir: true, ChangedLocal: true, Mode: Merge}, held},
		{"merge clean", Unit{Dir: true, InScope: true, ChangedLocal: true, Mode: Merge},
			Outcome{Placement: git.Placement{Commit: git.SourceMerged, Worktree: git.SourceMerged}}},
		{"merge conflict is held", Unit{Dir: true, InScope: true, ChangedLocal: true, Conflicted: true, Mode: Merge}, held},
		{"overlay clean", Unit{Dir: true, InScope: true, ChangedLocal: true, Mode: Overlay},
			Outcome{Placement: git.Placement{Commit: git.SourceRemote, Worktree: git.SourceMerged}}},
		{"overlay conflict is held", Unit{Dir: true, InScope: true, ChangedLocal: true, Conflicted: true, Mode: Overlay}, held},
		{"overwrite-remote ignores conflict", Unit{Dir: true, InScope: true, ChangedLocal: true, Conflicted: true, Mode: OverwriteRemote},
			Outcome{Placement: git.Placement{Commit: git.SourceLocal, Worktree: git.SourceUntouched}}},
		{"overwrite-local trashes", Unit{Dir: true, InScope: true, ChangedLocal: true, Conflicted: true, Mode: OverwriteLocal},
			Outcome{Placement: remote, Trash: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decide(tc.unit)
			if err != nil || got != tc.want {
				t.Fatalf("Decide = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

func TestDecide_RootFileConflictErrors(t *testing.T) {
	_, err := Decide(Unit{InScope: true, ChangedLocal: true, Conflicted: true, Mode: Merge})
	if !errors.Is(err, ErrRootConflict) {
		t.Fatalf("err = %v, want ErrRootConflict", err)
	}
}

func TestDecide_RemovedRemote(t *testing.T) {
	takeRemote := git.Placement{Commit: git.SourceRemote, Worktree: git.SourceRemote}
	cases := []struct {
		name string
		u    Unit
		want Outcome
	}{
		{"unchanged takes remote", Unit{Dir: true, InScope: true, RemovedRemote: true}, Outcome{Placement: takeRemote}},
		{"edited is trashed, not held", Unit{Dir: true, InScope: true, ChangedLocal: true, RemovedRemote: true, Conflicted: true, Mode: Merge}, Outcome{Placement: takeRemote, Trash: true}},
		{"overlay edits are trashed too", Unit{Dir: true, InScope: true, ChangedLocal: true, RemovedRemote: true, Mode: Overlay}, Outcome{Placement: takeRemote, Trash: true}},
		{"out of scope edited stays held", Unit{Dir: true, ChangedLocal: true, RemovedRemote: true, Conflicted: true, Mode: Merge}, Outcome{Placement: git.Placement{Commit: git.SourceRemote, Worktree: git.SourceUntouched}, Held: true}},
		{"overwrite-remote commits it back", Unit{Dir: true, InScope: true, ChangedLocal: true, RemovedRemote: true, Mode: OverwriteRemote}, Outcome{Placement: git.Placement{Commit: git.SourceLocal, Worktree: git.SourceUntouched}}},
	}
	for _, c := range cases {
		got, err := Decide(c.u)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Fatalf("%s: Decide = %+v, want %+v", c.name, got, c.want)
		}
	}
}
