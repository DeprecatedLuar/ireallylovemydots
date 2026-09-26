package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/engine"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/paths"
	"github.com/DeprecatedLuar/dotz/internal/state"
)

func collision(ns string) engine.Problem {
	return engine.Problem{Kind: engine.NamespaceCollision, Conflicting: &state.Key{Repo: "r", Namespace: ns}}
}

func TestEnableFooter_ForceClause(t *testing.T) {
	hard := []engine.Problem{{Kind: engine.LinkGuard}}
	occupied := []engine.Problem{{Kind: engine.RealFileCollision}}

	cases := []struct {
		name    string
		skipped [][]engine.Problem
		want    string
	}{
		{"all hard-blocked", [][]engine.Problem{hard, hard}, "0 enabled, 2 skipped."},
		{"all namespace collisions", [][]engine.Problem{{collision("A")}}, "0 enabled, 1 skipped. Try with --force to disable A."},
		{"mixed", [][]engine.Problem{{collision("A")}, occupied}, "0 enabled, 2 skipped. --force to override."},
		{"hard plus forceable collision", [][]engine.Problem{hard, {collision("A")}}, "0 enabled, 2 skipped. Try with --force to disable A."},
	}
	for _, c := range cases {
		if got := enableFooter(0, len(c.skipped), nil, c.skipped); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestEnableFooter_CollapsedClauseWithoutForce(t *testing.T) {
	hard := [][]engine.Problem{{{Kind: engine.LinkGuard}}}
	got := enableFooter(0, 1, []string{"krita"}, hard)
	if want := "0 enabled, 1 skipped. Run `dots krita` to see them."; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestProblemSummary_AllNamespaceCollisions(t *testing.T) {
	got := problemSummary([]engine.Problem{collision("a"), collision("a"), collision("c")})
	if got != "blocked by a, c" {
		t.Fatalf("got %q", got)
	}
}

func TestProblemSummary_DedupesByPath(t *testing.T) {
	link := "/home/u/.config/x"
	problems := []engine.Problem{
		{Kind: engine.RealFileCollision, Entry: manifest.Entry{Name: "f1"}, Path: link, Message: link + " already exists (link to ~/somewhere)"},
		{Kind: engine.RealFileCollision, Entry: manifest.Entry{Name: "f2"}, Path: link, Message: link + " already exists (link to ~/somewhere)"},
	}
	if collapsesToCount(problems) {
		t.Fatal("two entries behind one link must not collapse to a count")
	}
	if got := problemSummary(problems); !strings.Contains(got, "link to ~/somewhere") || strings.Contains(got, "destinations") {
		t.Fatalf("got %q", got)
	}
}

func TestEntryListing_DisabledNamespaceNamesBlockingNamespace(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dataDir, err := paths.Data()
	if err != nil {
		t.Fatal(err)
	}
	repoDir := filepath.Join(dataDir, "dotfiles")
	home := t.TempDir()
	xDest := filepath.Join(home, ".config", "x")

	aDir := filepath.Join(repoDir, "a")
	bDir := filepath.Join(repoDir, "b")
	for _, d := range []string{filepath.Join(aDir, "dir"), bDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bDir, "f"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	aKey := state.Key{Repo: "dotfiles", Namespace: "a"}
	bKey := state.Key{Repo: "dotfiles", Namespace: "b"}
	s := state.State{Entries: map[state.Key]state.Entry{}}
	if _, err := engine.Enable(aKey, repoDir, aDir, "a", []manifest.Entry{{Name: "dir", Dest: xDest}}, s, nil); err != nil {
		t.Fatal(err)
	}

	entries := []manifest.Entry{{Name: "f", Dest: filepath.Join(xDest, "f")}}
	rows, _, err := entryListing(bKey, s, bDir, entries, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !strings.Contains(rows[0].Name, "blocked by a") || strings.Contains(rows[0].Name, "real file") {
		t.Fatalf("rows = %+v", rows)
	}
}
