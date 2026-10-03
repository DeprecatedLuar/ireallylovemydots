package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DeprecatedLuar/ireallylovemydots/internal/manifest"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/paths"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/state"
)

const parentLinkRepo = "dotfiles"

// parentLinkEnv points every XDG directory at a temp dir and returns the
// repository directory inside the data directory plus a fresh home.
func parentLinkEnv(t *testing.T) (repoDir, home string) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dataDir, err := paths.Data()
	if err != nil {
		t.Fatal(err)
	}
	repoDir = filepath.Join(dataDir, parentLinkRepo)
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}
	return repoDir, t.TempDir()
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(p))
	if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestPreflightAndEnable_ParentLinkedToEnabledNamespace(t *testing.T) {
	repoDir, home := parentLinkEnv(t)
	xDest := filepath.Join(home, ".config", "x")

	aDir := filepath.Join(repoDir, "a")
	mustMkdir(t, filepath.Join(aDir, "dir"))
	mustWrite(t, filepath.Join(aDir, "dir", "keep"))
	aKey := state.Key{Repo: parentLinkRepo, Namespace: "a"}
	s := state.State{Entries: map[state.Key]state.Entry{}}
	if _, err := Enable(aKey, repoDir, aDir, "a", []manifest.Entry{{Name: "dir", Dest: xDest}}, s, nil); err != nil {
		t.Fatalf("enable a: %v", err)
	}

	bDir := filepath.Join(repoDir, "b")
	mustWrite(t, filepath.Join(bDir, "f"))
	bKey := state.Key{Repo: parentLinkRepo, Namespace: "b"}
	bDest := filepath.Join(xDest, "f")
	entries := []manifest.Entry{{Name: "f", Dest: bDest}}

	problems, err := Preflight(bKey, bDir, entries, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 || problems[0].Kind != NamespaceCollision || problems[0].Conflicting == nil || *problems[0].Conflicting != aKey {
		t.Fatalf("expected one NamespaceCollision naming a, got %+v", problems)
	}

	res, err := Enable(bKey, repoDir, bDir, "b", entries, s, problems)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Disabled) != 1 || res.Disabled[0] != aKey {
		t.Fatalf("Disabled = %+v, want [a]", res.Disabled)
	}
	if s.Entries[aKey].Enabled {
		t.Fatal("a should be disabled")
	}
	info, err := os.Lstat(xDest)
	if err != nil || !info.IsDir() {
		t.Fatalf("expected %s to be a real directory, err=%v", xDest, err)
	}
	if target, err := os.Readlink(bDest); err != nil || target != filepath.Join(bDir, "f") {
		t.Fatalf("expected f linked into b, got %q err=%v", target, err)
	}
	if _, err := os.Stat(filepath.Join(aDir, "dir", "keep")); err != nil {
		t.Fatalf("a's payload must be untouched: %v", err)
	}
}

func TestPreflightAndEnable_TwoLinkedAncestors(t *testing.T) {
	repoDir, home := parentLinkEnv(t)

	aDir := filepath.Join(repoDir, "a")
	cDir := filepath.Join(repoDir, "c")
	mustMkdir(t, filepath.Join(aDir, "d1"))
	mustMkdir(t, filepath.Join(cDir, "d2"))
	link1 := filepath.Join(home, "link1")
	if err := os.Symlink(filepath.Join(aDir, "d1"), link1); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(cDir, "d2"), filepath.Join(aDir, "d1", "sub")); err != nil {
		t.Fatal(err)
	}
	aKey := state.Key{Repo: parentLinkRepo, Namespace: "a"}
	cKey := state.Key{Repo: parentLinkRepo, Namespace: "c"}
	s := state.State{Entries: map[state.Key]state.Entry{
		// No recorded destinations: the index claims nothing, so only the
		// filesystem walk can name the two namespaces.
		aKey: {Enabled: true},
		cKey: {Enabled: true},
	}}

	bDir := filepath.Join(repoDir, "b")
	mustWrite(t, filepath.Join(bDir, "f"))
	bKey := state.Key{Repo: parentLinkRepo, Namespace: "b"}
	bDest := filepath.Join(link1, "sub", "f")
	entries := []manifest.Entry{{Name: "f", Dest: bDest}}

	problems, err := Preflight(bKey, bDir, entries, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 2 || problems[0].Kind != NamespaceCollision || problems[1].Kind != NamespaceCollision {
		t.Fatalf("expected two NamespaceCollisions, got %+v", problems)
	}
	res, err := Enable(bKey, repoDir, bDir, "b", entries, s, problems)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Disabled) != 2 {
		t.Fatalf("Disabled = %+v, want both", res.Disabled)
	}
	if _, err := os.Readlink(bDest); err != nil {
		t.Fatalf("expected f linked: %v", err)
	}
}

func TestPreflightAndEnable_ParentLinkNamingNoEnabledNamespace(t *testing.T) {
	cases := map[string]func(repoDir string) string{
		"disabled namespace": func(repoDir string) string { return filepath.Join(repoDir, "off") },
		"repository root":    func(repoDir string) string { return repoDir },
	}
	for name, target := range cases {
		t.Run(name, func(t *testing.T) {
			repoDir, home := parentLinkEnv(t)
			mustWrite(t, filepath.Join(repoDir, "off", "marker"))
			linkPath := filepath.Join(home, "x")
			linkTarget := target(repoDir)
			if err := os.Symlink(linkTarget, linkPath); err != nil {
				t.Fatal(err)
			}
			bDir := filepath.Join(repoDir, "b")
			mustWrite(t, filepath.Join(bDir, "f"))
			bKey := state.Key{Repo: parentLinkRepo, Namespace: "b"}
			bDest := filepath.Join(linkPath, "f")
			entries := []manifest.Entry{{Name: "f", Dest: bDest}}
			s := emptyState()

			problems, err := Preflight(bKey, bDir, entries, s)
			if err != nil {
				t.Fatal(err)
			}
			if len(problems) != 1 || problems[0].Kind != RealFileCollision || problems[0].Path != linkPath {
				t.Fatalf("expected RealFileCollision at the link, got %+v", problems)
			}
			if _, err := Enable(bKey, repoDir, bDir, "b", entries, s, problems); err != nil {
				t.Fatal(err)
			}
			info, err := os.Lstat(linkPath)
			if err != nil || !info.IsDir() {
				t.Fatalf("expected the link replaced by a real directory, err=%v", err)
			}
			if _, err := os.Stat(filepath.Join(repoDir, "off", "marker")); err != nil {
				t.Fatalf("the link's target must survive: %v", err)
			}
			if _, err := os.Readlink(bDest); err != nil {
				t.Fatalf("expected f linked: %v", err)
			}
		})
	}
}

func TestPreflight_ParentLinkedOutsideDataDir_PlainRealFileCollision(t *testing.T) {
	repoDir, home := parentLinkEnv(t)
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "f"))
	linkPath := filepath.Join(home, "x")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Fatal(err)
	}
	bDir := filepath.Join(repoDir, "b")
	mustWrite(t, filepath.Join(bDir, "f"))
	bDest := filepath.Join(linkPath, "f")

	problems, err := Preflight(state.Key{Repo: parentLinkRepo, Namespace: "b"}, bDir, []manifest.Entry{{Name: "f", Dest: bDest}}, emptyState())
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 || problems[0].Kind != RealFileCollision || problems[0].Path != bDest {
		t.Fatalf("expected RealFileCollision at the destination, got %+v", problems)
	}
}
