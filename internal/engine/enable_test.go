package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/state"
)

// gitRepoWithNamespaces builds a small committed git repository holding two
// namespaces, each with one directory entry, standing in for a repository
// cloned blobless with no checkout — the fixture Clone (Phase 3) already
// tests against.
func gitRepoWithNamespaces(t *testing.T, home string) (repoDir string, nsAEntries, nsBEntries []manifest.Entry) {
	t.Helper()
	repoDir = t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")

	destA := filepath.Join(home, ".config", "aaa")
	destB := filepath.Join(home, ".config", "bbb")

	for name, dest := range map[string]string{"aaa": destA, "bbb": destB} {
		nsDir := filepath.Join(repoDir, name)
		payload := filepath.Join(nsDir, name)
		if err := os.MkdirAll(payload, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(payload, "seed"), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
		m := manifest.Manifest{Entries: []manifest.Entry{{Name: name, Dest: dest}}}
		if err := manifest.Write(nsDir, m); err != nil {
			t.Fatal(err)
		}
	}
	run("add", ".")
	run("commit", "-m", "init")

	// A real Clone (Phase 8.7) is sparse with an empty cone from birth;
	// `sparse-checkout init --cone` here reproduces that starting point on
	// this fixture, which was built fully checked out for convenience —
	// materialize's Add now requires sparse checkout to already be
	// initialized, matching what every real clone provides.
	run("sparse-checkout", "init", "--cone")

	return repoDir, []manifest.Entry{{Name: "aaa", Dest: destA}}, []manifest.Entry{{Name: "bbb", Dest: destB}}
}

func TestEnable_MaterializesOnlyTargetNamespace(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	home := t.TempDir()
	repoDir, entriesA, _ := gitRepoWithNamespaces(t, home)

	nsADir := filepath.Join(repoDir, "aaa")
	nsBDir := filepath.Join(repoDir, "bbb")

	// gitRepoWithNamespaces' sparse-checkout init --cone already leaves the
	// worktree empty, matching a fresh Clone's empty cone.
	if _, err := os.Stat(nsADir); !os.IsNotExist(err) {
		t.Fatalf("expected the fixture's empty cone to leave aaa/ unmaterialized, got err=%v", err)
	}
	if _, err := os.Stat(nsBDir); !os.IsNotExist(err) {
		t.Fatalf("expected the fixture's empty cone to leave bbb/ unmaterialized, got err=%v", err)
	}

	key := state.Key{Repo: "dotfiles", Namespace: "aaa"}
	s := state.State{Entries: map[state.Key]state.Entry{}}
	if _, err := Enable(key, repoDir, nsADir, "aaa", entriesA, s, nil); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	if _, err := os.Stat(nsADir); err != nil {
		t.Fatalf("expected the target namespace's folder to be materialized: %v", err)
	}
	if _, err := os.Stat(nsBDir); !os.IsNotExist(err) {
		t.Fatalf("expected the sibling namespace's folder to stay absent from the worktree, got err=%v", err)
	}

	info, err := os.Lstat(entriesA[0].Dest)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected a symlink at the enabled entry's destination: %v", err)
	}
}

func TestEnable_DirectoryEntry_OneSymlinkAndPayloadWritesVisible(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	home := t.TempDir()
	nsDir := t.TempDir()
	payload := filepath.Join(nsDir, "nvim")
	if err := os.MkdirAll(payload, 0755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(home, ".config", "nvim")
	entries := []manifest.Entry{{Name: "nvim", Dest: dest}}

	key := state.Key{Repo: "dotfiles", Namespace: "editors"}
	s := state.State{Entries: map[state.Key]state.Entry{}}
	if _, err := Enable(key, nsDir, nsDir, "editors", entries, s, nil); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	info, err := os.Lstat(dest)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected exactly one symlink at %s", dest)
	}

	if err := os.WriteFile(filepath.Join(payload, "init.lua"), []byte("-- x"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "init.lua")); err != nil {
		t.Fatalf("expected a file added to the payload afterwards to be visible at the destination: %v", err)
	}
}

func TestEnable_InjectedFailureOnSeventhLink_RollsBackEverythingAndWritesNoState(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if runtime.GOOS == "windows" {
		t.Skip("permission bits behave differently on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses permission checks")
	}

	home := t.TempDir()
	nsDir := t.TempDir()

	const total = 10
	const failAt = 7 // 1-indexed: the 7th entry's link must fail.

	var entries []manifest.Entry
	var blockedDir string
	for i := 1; i <= total; i++ {
		// Every entry gets its own directory, all at the same path depth,
		// so sortByDepth preserves this input order and the failure lands
		// on exactly the seventh link attempted.
		dirName := filepath.Join(home, fmt.Sprintf("dir%d", i))
		if i == failAt {
			blockedDir = dirName
		}
		if err := os.MkdirAll(dirName, 0755); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, manifest.Entry{Name: "target", Dest: filepath.Join(dirName, "target")})
	}

	// Lock the seventh entry's parent directory after creating it, so
	// os.MkdirAll (already satisfied) succeeds but creating the new
	// directory entry for its symlink fails with a real permission error —
	// a genuine filesystem seam, not a mocked one.
	if err := os.Chmod(blockedDir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(blockedDir, 0755) })

	key := state.Key{Repo: "dotfiles", Namespace: "editors"}
	s := state.State{Entries: map[state.Key]state.Entry{}}

	if _, err := Enable(key, nsDir, nsDir, "editors", entries, s, nil); err == nil {
		t.Fatal("expected the seventh link's permission failure to surface as an error")
	}

	for i, e := range entries {
		if i+1 == failAt {
			continue
		}
		if _, err := os.Lstat(e.Dest); !os.IsNotExist(err) {
			t.Fatalf("expected entry %d's link to be rolled back, got err=%v", i+1, err)
		}
	}

	if _, ok := s.Entries[key]; ok {
		t.Fatalf("expected no state entry after a rolled-back enable, got %+v", s.Entries[key])
	}
}

// TestEnable_InjectedFailureAfterAbsorbingSymlink_RestoresOriginalTarget
// covers rollback's other half: an entry whose destination held a live
// symlink gets it cleared before Enable links its own payload there; if a
// later entry in the same batch then fails, rollback must recreate that
// original symlink exactly as found, not leave the destination missing.
func TestEnable_InjectedFailureAfterAbsorbingSymlink_RestoresOriginalTarget(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if runtime.GOOS == "windows" {
		t.Skip("permission bits behave differently on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses permission checks")
	}

	home := t.TempDir()
	nsDir := t.TempDir()

	firstPayload := filepath.Join(nsDir, "first")
	if err := os.MkdirAll(firstPayload, 0755); err != nil {
		t.Fatal(err)
	}
	originalTarget := filepath.Join(home, "elsewhere")
	if err := os.WriteFile(originalTarget, []byte("z"), 0644); err != nil {
		t.Fatal(err)
	}
	firstDest := filepath.Join(home, "first")
	if err := os.Symlink(originalTarget, firstDest); err != nil {
		t.Fatal(err)
	}

	secondPayload := filepath.Join(nsDir, "second")
	if err := os.MkdirAll(secondPayload, 0755); err != nil {
		t.Fatal(err)
	}
	blockedDir := filepath.Join(home, "blocked")
	if err := os.MkdirAll(blockedDir, 0755); err != nil {
		t.Fatal(err)
	}
	secondDest := filepath.Join(blockedDir, "second")
	if err := os.Chmod(blockedDir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(blockedDir, 0755) })

	entries := []manifest.Entry{
		{Name: "first", Dest: firstDest},
		{Name: "second", Dest: secondDest},
	}
	key := state.Key{Repo: "dotfiles", Namespace: "editors"}
	s := state.State{Entries: map[state.Key]state.Entry{}}

	if _, err := Enable(key, nsDir, nsDir, "editors", entries, s, nil); err == nil {
		t.Fatal("expected the second entry's permission failure to surface as an error")
	}

	got := readLinkEnableTest(t, firstDest)
	if got != originalTarget {
		t.Fatalf("expected %s restored pointing at %s, got %s", firstDest, originalTarget, got)
	}
	if _, ok := s.Entries[key]; ok {
		t.Fatalf("expected no state entry after a rolled-back enable, got %+v", s.Entries[key])
	}
}

func readLinkEnableTest(t *testing.T, path string) string {
	t.Helper()
	target, err := os.Readlink(path)
	if err != nil {
		t.Fatalf("readlink %s: %v", path, err)
	}
	return target
}

func TestEnable_MaterializeReusesExistingFolderWithNoGitOperation(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	home := t.TempDir()
	nsDir := t.TempDir()
	payload := filepath.Join(nsDir, "nvim")
	if err := os.MkdirAll(payload, 0755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(home, ".config", "nvim")
	entries := []manifest.Entry{{Name: "nvim", Dest: dest}}

	// repoDir is not a git repository at all: if materialize ever shelled
	// out to git for an already-materialized namespace, this would fail.
	repoDir := filepath.Dir(nsDir)

	key := state.Key{Repo: "dotfiles", Namespace: "editors"}
	s := state.State{Entries: map[state.Key]state.Entry{}}
	if _, err := Enable(key, repoDir, nsDir, "editors", entries, s, nil); err != nil {
		t.Fatalf("Enable on an already-materialized namespace touched git: %v", err)
	}

	if err := Disable(key, s); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if _, err := Enable(key, repoDir, nsDir, "editors", entries, s, nil); err != nil {
		t.Fatalf("re-Enable after disable touched git: %v", err)
	}
}
