package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/state"
)

// TestRmNamespaces_BatchInSameRepo_DoesNotSelfBlockOnGitSafety reproduces
// the code-review-flagged bug: git safety must be checked once per affected
// repository before anything in that repo's batch is trashed, not
// re-checked per namespace mid-loop. Trashing one namespace's folder is a
// raw filesystem move that leaves the repo's git status dirty, so a
// per-namespace check would see that self-inflicted dirt and wrongly refuse
// the next namespace in the same batch.
func TestRmNamespaces_BatchInSameRepo_DoesNotSelfBlockOnGitSafety(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	reg := manifest.Registry{Repos: []manifest.Repo{
		{Name: "dotfiles", Owner: "someone", URL: "https://example.com/someone/dotfiles"},
	}}
	if err := manifest.WriteRegistry(reg); err != nil {
		t.Fatal(err)
	}
	repoDir := filepath.Join(dataHome, "ireallylovemydots", "dotfiles")

	destA := filepath.Join(home, ".config", "aaa")
	destB := filepath.Join(home, ".config", "bbb")
	for nsName, dest := range map[string]string{"aaa": destA, "bbb": destB} {
		nsDir := filepath.Join(repoDir, nsName)
		if err := os.MkdirAll(filepath.Join(nsDir, nsName), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(nsDir, nsName, "seed"), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := manifest.Write(nsDir, manifest.Manifest{Entries: []manifest.Entry{{Name: nsName, Dest: dest}}}); err != nil {
			t.Fatal(err)
		}
	}

	runGit(t, repoDir, "init", "-b", "main")
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "init")
	runGit(t, repoDir, "remote", "add", "origin", "https://example.com/someone/dotfiles")

	if err := HandleNamespace([]string{"rm", "aaa", "bbb"}, shared.Flags{Yes: true}); err != nil {
		t.Fatalf("namespace rm aaa bbb (batch in same repo): %v", err)
	}

	for _, dest := range []string{destA, destB} {
		if _, err := os.Lstat(dest); !os.IsNotExist(err) {
			t.Fatalf("expected nothing written to %s, got err=%v", dest, err)
		}
	}
	if _, err := os.Stat(filepath.Join(repoDir, "aaa")); !os.IsNotExist(err) {
		t.Fatal("expected namespace aaa's folder removed")
	}
	if _, err := os.Stat(filepath.Join(repoDir, "bbb")); !os.IsNotExist(err) {
		t.Fatal("expected namespace bbb's folder removed too, not self-blocked by aaa's own removal")
	}
}

// TestRmEntry_Default_TrashesPayloadWritesNothingHome covers concept.md's
// new default: rm trashes and writes nothing to the home directory.
func TestRmEntry_Default_TrashesPayloadWritesNothingHome(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".config", "nvim")
	entries := []manifest.Entry{{Name: "nvim", Dest: dest}}
	dataDir, _, nsDir := registerRepoWithNamespace(t, "editors", entries)

	if err := HandleNamespace([]string{"editors", "rm", dest}, shared.Flags{Yes: true}); err != nil {
		t.Fatalf("namespace editors rm %s: %v", dest, err)
	}

	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatalf("expected nothing written to the destination, got err=%v", err)
	}

	m, err := manifest.Read(nsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entries) != 0 {
		t.Fatalf("expected no manifest entries left, got %+v", m.Entries)
	}

	trashFiles := filepath.Join(filepath.Dir(dataDir), "Trash", "files")
	dirEntries, err := os.ReadDir(trashFiles)
	if err != nil {
		t.Fatalf("read trash files dir: %v", err)
	}
	if len(dirEntries) == 0 {
		t.Fatal("expected the trashed payload recoverable from the XDG trash")
	}
}

func TestRmEntry_Restore_WritesRealFileToDestination(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".config", "nvim")
	entries := []manifest.Entry{{Name: "nvim", Dest: dest}}
	_, _, nsDir := registerRepoWithNamespace(t, "editors", entries)

	if err := HandleNamespace([]string{"editors", "rm", dest}, shared.Flags{Yes: true, Restore: true}); err != nil {
		t.Fatalf("namespace editors rm %s --restore: %v", dest, err)
	}

	info, err := os.Lstat(dest)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected a real directory left at %s, got err=%v", dest, err)
	}

	m, err := manifest.Read(nsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entries) != 0 {
		t.Fatalf("expected no manifest entries left, got %+v", m.Entries)
	}
}

func TestRmNamespace_Default_TrashesFolderWritesNothingHome(t *testing.T) {
	home := t.TempDir()
	destA := filepath.Join(home, ".config", "aaa")
	destB := filepath.Join(home, ".config", "bbb")
	entries := []manifest.Entry{{Name: "aaa", Dest: destA}, {Name: "bbb", Dest: destB}}
	_, _, nsDir := registerRepoWithNamespace(t, "editors", entries)

	if err := HandleNamespace([]string{"rm", "editors"}, shared.Flags{Yes: true}); err != nil {
		t.Fatalf("namespace rm editors: %v", err)
	}

	for _, dest := range []string{destA, destB} {
		if _, err := os.Lstat(dest); !os.IsNotExist(err) {
			t.Fatalf("expected nothing written to %s, got err=%v", dest, err)
		}
	}
	if _, err := os.Stat(nsDir); !os.IsNotExist(err) {
		t.Fatalf("expected the namespace folder removed, got err=%v", err)
	}
}

func TestRmNamespace_Restore_DisabledNamespace_WritesFilesToDestinations(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".config", "nvim")
	entries := []manifest.Entry{{Name: "nvim", Dest: dest}}
	registerRepoWithNamespace(t, "editors", entries)

	// Never enabled: dest starts out absent.
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatalf("expected dest absent before rm, got err=%v", err)
	}

	if err := HandleNamespace([]string{"rm", "editors"}, shared.Flags{Yes: true, Restore: true}); err != nil {
		t.Fatalf("namespace rm editors --restore (disabled): %v", err)
	}

	info, err := os.Lstat(dest)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected a real directory written to the previously-empty destination: %v", err)
	}
}

func TestRmNamespace_EnabledNamespace_DefaultRemovesSymlinkTrashesFolderNoDangling(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".config", "nvim")
	entries := []manifest.Entry{{Name: "nvim", Dest: dest}}
	_, _, nsDir := registerRepoWithNamespace(t, "editors", entries)

	if err := enableNamespace("editors", shared.Flags{}); err != nil {
		t.Fatalf("enableNamespace: %v", err)
	}
	info, err := os.Lstat(dest)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected a symlink after enable: %v", err)
	}

	if err := HandleNamespace([]string{"rm", "editors"}, shared.Flags{Yes: true}); err != nil {
		t.Fatalf("namespace rm editors (enabled): %v", err)
	}

	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatalf("expected no dangling symlink and nothing written to %s, got err=%v", dest, err)
	}
	if _, err := os.Stat(nsDir); !os.IsNotExist(err) {
		t.Fatalf("expected the namespace folder removed, got err=%v", err)
	}

	s, err := state.Read()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Entries[state.Key{Repo: "dotfiles", Namespace: "editors"}]; ok {
		t.Fatalf("expected the namespace's state entry cleared after rm")
	}
}

func TestRmNamespace_Restore_EnabledNamespace_RemovesSymlinkAndWritesRealFile(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".config", "nvim")
	entries := []manifest.Entry{{Name: "nvim", Dest: dest}}
	_, _, nsDir := registerRepoWithNamespace(t, "editors", entries)

	if err := enableNamespace("editors", shared.Flags{}); err != nil {
		t.Fatalf("enableNamespace: %v", err)
	}

	if err := HandleNamespace([]string{"rm", "editors"}, shared.Flags{Yes: true, Restore: true}); err != nil {
		t.Fatalf("namespace rm editors --restore (enabled): %v", err)
	}

	info, err := os.Lstat(dest)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected the symlink replaced by a real directory, got err=%v", err)
	}
	if _, err := os.Stat(nsDir); !os.IsNotExist(err) {
		t.Fatalf("expected the namespace folder removed, got err=%v", err)
	}
}

func TestRmNamespace_Purge_LeavesNothingInTrash(t *testing.T) {
	home := t.TempDir()
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	reg := manifest.Registry{Repos: []manifest.Repo{
		{Name: "dotfiles", Owner: "someone", URL: "https://example.com/someone/dotfiles"},
	}}
	if err := manifest.WriteRegistry(reg); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(dataHome, "ireallylovemydots")
	nsDir := filepath.Join(dataDir, "dotfiles", "editors")
	payload := filepath.Join(nsDir, "nvim")
	if err := os.MkdirAll(payload, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "init.lua"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(home, ".config", "nvim")
	if err := manifest.Write(nsDir, manifest.Manifest{Entries: []manifest.Entry{{Name: "nvim", Dest: dest}}}); err != nil {
		t.Fatal(err)
	}

	if err := HandleNamespace([]string{"rm", "editors"}, shared.Flags{Yes: true, Purge: true}); err != nil {
		t.Fatalf("namespace rm editors --purge: %v", err)
	}

	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatalf("expected nothing written to the destination under --purge, got err=%v", err)
	}
	if _, err := os.Stat(nsDir); !os.IsNotExist(err) {
		t.Fatalf("expected the namespace folder removed, got err=%v", err)
	}

	trashFiles := filepath.Join(dataHome, "Trash", "files")
	dirEntries, err := os.ReadDir(trashFiles)
	if err == nil && len(dirEntries) != 0 {
		t.Fatalf("expected nothing left in the trash after --purge, got %+v", dirEntries)
	}
}

func TestRmNamespace_RestorePurge_RestoresFirstThenLeavesNothingInTrash(t *testing.T) {
	home := t.TempDir()
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	reg := manifest.Registry{Repos: []manifest.Repo{
		{Name: "dotfiles", Owner: "someone", URL: "https://example.com/someone/dotfiles"},
	}}
	if err := manifest.WriteRegistry(reg); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(dataHome, "ireallylovemydots")
	nsDir := filepath.Join(dataDir, "dotfiles", "editors")
	payload := filepath.Join(nsDir, "nvim")
	if err := os.MkdirAll(payload, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "init.lua"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(home, ".config", "nvim")
	if err := manifest.Write(nsDir, manifest.Manifest{Entries: []manifest.Entry{{Name: "nvim", Dest: dest}}}); err != nil {
		t.Fatal(err)
	}

	if err := HandleNamespace([]string{"rm", "editors"}, shared.Flags{Yes: true, Restore: true, Purge: true}); err != nil {
		t.Fatalf("namespace rm editors --restore --purge: %v", err)
	}

	info, err := os.Lstat(dest)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected the restored real file at %s, got err=%v", dest, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "init.lua")); err != nil {
		t.Fatalf("expected the restored payload's contents at the destination: %v", err)
	}

	trashFiles := filepath.Join(dataHome, "Trash", "files")
	dirEntries, err := os.ReadDir(trashFiles)
	if err == nil && len(dirEntries) != 0 {
		t.Fatalf("expected nothing left in the trash after --restore --purge, got %+v", dirEntries)
	}
}

func TestRestoreEntries_NonInteractiveOccupiedDestination_ErrorsNamingForce(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("real content"), 0644); err != nil {
		t.Fatal(err)
	}
	entries := []manifest.Entry{{Name: "nvim", Dest: dest}}
	registerRepoWithNamespace(t, "editors", entries)

	err := HandleNamespace([]string{"editors", "rm", dest}, shared.Flags{Yes: true, Restore: true})
	if err == nil {
		t.Fatal("expected a non-interactive occupied-destination restore to fail")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected the error to name --force, got: %v", err)
	}

	info, statErr := os.Lstat(dest)
	if statErr != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected the occupied destination left untouched: %v", statErr)
	}
}

func TestRmEntry_RestoreForce_TrashesOccupantAndRestoresOurs(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	home := t.TempDir()
	dest := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("occupant"), 0644); err != nil {
		t.Fatal(err)
	}
	entries := []manifest.Entry{{Name: "nvim", Dest: dest}}
	_, _, nsDir := registerRepoWithNamespace(t, "editors", entries)
	if err := os.MkdirAll(filepath.Join(nsDir, "nvim"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nsDir, "nvim", "seed"), []byte("ours"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := HandleNamespace([]string{"editors", "rm", dest}, shared.Flags{Force: true, Yes: true, Restore: true}); err != nil {
		t.Fatalf("namespace editors rm %s --restore --force: %v", dest, err)
	}

	if _, err := os.Stat(filepath.Join(dest, "seed")); err != nil {
		t.Fatalf("expected our restored payload at the destination: %v", err)
	}
}

func TestRmRepo_Default_TrashesEverythingWritesNothingHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	reg := manifest.Registry{Repos: []manifest.Repo{
		{Name: "dotfiles", Owner: "someone", URL: "https://example.com/someone/dotfiles"},
	}}
	if err := manifest.WriteRegistry(reg); err != nil {
		t.Fatal(err)
	}
	repoDir := filepath.Join(dataHome, "ireallylovemydots", "dotfiles")

	destA := filepath.Join(home, ".config", "aaa")
	destB := filepath.Join(home, ".config", "bbb")
	for nsName, dest := range map[string]string{"aaa": destA, "bbb": destB} {
		nsDir := filepath.Join(repoDir, nsName)
		if err := os.MkdirAll(filepath.Join(nsDir, nsName), 0755); err != nil {
			t.Fatal(err)
		}
		if err := manifest.Write(nsDir, manifest.Manifest{Entries: []manifest.Entry{{Name: nsName, Dest: dest}}}); err != nil {
			t.Fatal(err)
		}
	}

	if err := HandleRepo([]string{"rm", "dotfiles"}, shared.Flags{Yes: true}); err != nil {
		t.Fatalf("repo rm dotfiles: %v", err)
	}

	for _, dest := range []string{destA, destB} {
		if _, err := os.Lstat(dest); !os.IsNotExist(err) {
			t.Fatalf("expected nothing written to %s, got err=%v", dest, err)
		}
	}
	if _, err := os.Stat(repoDir); !os.IsNotExist(err) {
		t.Fatalf("expected the repository clone removed, got err=%v", err)
	}

	after, err := manifest.ReadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Repos) != 0 {
		t.Fatalf("expected the repository dropped from the registry, got %+v", after.Repos)
	}
}

func TestRm_NonInteractiveWithoutYes_HardErrorsChangesNothing(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".config", "nvim")
	entries := []manifest.Entry{{Name: "nvim", Dest: dest}}
	_, _, nsDir := registerRepoWithNamespace(t, "editors", entries)

	err := HandleNamespace([]string{"rm", "editors"}, shared.Flags{})
	if err == nil {
		t.Fatal("expected a non-interactive rm without -y to fail")
	}
	if _, statErr := os.Stat(nsDir); statErr != nil {
		t.Fatalf("expected the namespace folder untouched, got err=%v", statErr)
	}
	m, err := manifest.Read(nsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entries) != 1 {
		t.Fatalf("expected the manifest untouched, got %+v", m.Entries)
	}
}
