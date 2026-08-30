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

func TestRmEntry_LeavesRealDirectoryAndNoManifestEntry(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".config", "nvim")
	entries := []manifest.Entry{{Name: "nvim", Dest: dest}}
	_, _, nsDir := registerRepoWithNamespace(t, "editors", entries)

	if err := HandleNamespace([]string{"editors", "rm", dest}, shared.Flags{}); err != nil {
		t.Fatalf("namespace editors rm %s: %v", dest, err)
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

func TestRmNamespace_RestoresEveryEntryThenRemovesFolder(t *testing.T) {
	home := t.TempDir()
	destA := filepath.Join(home, ".config", "aaa")
	destB := filepath.Join(home, ".config", "bbb")
	entries := []manifest.Entry{{Name: "aaa", Dest: destA}, {Name: "bbb", Dest: destB}}
	dataDir, _, nsDir := registerRepoWithNamespace(t, "editors", entries)

	if err := HandleNamespace([]string{"rm", "editors"}, shared.Flags{}); err != nil {
		t.Fatalf("namespace rm editors: %v", err)
	}

	for _, dest := range []string{destA, destB} {
		info, err := os.Lstat(dest)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("expected a real directory at %s, got err=%v", dest, err)
		}
	}
	if _, err := os.Stat(nsDir); !os.IsNotExist(err) {
		t.Fatalf("expected the namespace folder removed, got err=%v", err)
	}
	_ = dataDir
}

func TestRmNamespace_DisabledNamespace_StillWritesFilesToDestinations(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".config", "nvim")
	entries := []manifest.Entry{{Name: "nvim", Dest: dest}}
	registerRepoWithNamespace(t, "editors", entries)

	// Never enabled: dest starts out absent.
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatalf("expected dest absent before rm, got err=%v", err)
	}

	if err := HandleNamespace([]string{"rm", "editors"}, shared.Flags{}); err != nil {
		t.Fatalf("namespace rm editors (disabled): %v", err)
	}

	info, err := os.Lstat(dest)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected a real directory written to the previously-empty destination: %v", err)
	}
}

func TestRmNamespace_EnabledNamespace_RemovesSymlinkAndFolder(t *testing.T) {
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

	if err := HandleNamespace([]string{"rm", "editors"}, shared.Flags{}); err != nil {
		t.Fatalf("namespace rm editors (enabled): %v", err)
	}

	info, err = os.Lstat(dest)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected the symlink replaced by a real directory, got err=%v", err)
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

func TestRmNamespace_Purge_TrashesInsteadOfRestoring(t *testing.T) {
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

	if err := HandleNamespace([]string{"rm", "editors"}, shared.Flags{Purge: true}); err != nil {
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
	if err != nil {
		t.Fatalf("read trash files dir: %v", err)
	}
	if len(dirEntries) == 0 {
		t.Fatal("expected the purged payload recoverable from the XDG trash")
	}
}

func TestRestoreEntries_NonInteractiveOccupiedDestination_ErrorsNamingBothChoices(t *testing.T) {
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

	err := HandleNamespace([]string{"editors", "rm", dest}, shared.Flags{})
	if err == nil {
		t.Fatal("expected a non-interactive occupied-destination removal to fail")
	}
	if !strings.Contains(err.Error(), "--force") || !strings.Contains(err.Error(), "--purge") {
		t.Fatalf("expected the error to name both --force and --purge, got: %v", err)
	}

	info, statErr := os.Lstat(dest)
	if statErr != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected the occupied destination left untouched: %v", statErr)
	}
}

func TestRmEntry_Force_TrashesOccupantAndRestoresOurs(t *testing.T) {
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

	if err := HandleNamespace([]string{"editors", "rm", dest}, shared.Flags{Force: true}); err != nil {
		t.Fatalf("namespace editors rm %s --force: %v", dest, err)
	}

	if _, err := os.Stat(filepath.Join(dest, "seed")); err != nil {
		t.Fatalf("expected our restored payload at the destination: %v", err)
	}
}

func TestRmRepo_RestoresEveryNamespaceThenRemovesCloneAndRegistryEntry(t *testing.T) {
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

	if err := HandleRepo([]string{"rm", "dotfiles"}, shared.Flags{}); err != nil {
		t.Fatalf("repo rm dotfiles: %v", err)
	}

	for _, dest := range []string{destA, destB} {
		info, err := os.Lstat(dest)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("expected a real directory at %s, got err=%v", dest, err)
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
