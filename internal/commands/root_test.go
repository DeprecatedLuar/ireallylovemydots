package commands

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DeprecatedLuar/ireallylovemydots/internal/commands/shared"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/manifest"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/namespace"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/paths"
)

// setupRootRepo registers one committed git repository whose root is a
// namespace named rootName with default destination dest, holding files at
// its root, beside one empty folder namespace "shell".
func setupRootRepo(t *testing.T, repoName, rootName, dest string, files ...string) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	repoDir := filepath.Join(dataHome, "ireallylovemydots", repoName)
	if err := os.MkdirAll(filepath.Join(repoDir, "shell"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(repoDir, f), []byte("content of "+f), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := manifest.Write(repoDir, manifest.Manifest{Name: rootName, Dest: dest}); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Write(filepath.Join(repoDir, "shell"), manifest.Manifest{}); err != nil {
		t.Fatal(err)
	}
	syncGitRun(t, repoDir, "init", "-b", "main")
	syncGitRun(t, repoDir, "config", "user.name", "dots test")
	syncGitRun(t, repoDir, "config", "user.email", "dots@example.invalid")
	syncGitRun(t, repoDir, "add", "-A")
	syncGitRun(t, repoDir, "commit", "-m", "seed")

	reg := manifest.Registry{Repos: []manifest.Repo{{Name: repoName, Owner: "someone", URL: "https://example.com/someone/" + repoName}}}
	if err := manifest.WriteRegistry(reg); err != nil {
		t.Fatal(err)
	}
	return repoDir
}

func TestResolve_RootNamespaceLivesAtTheRepositoryRoot(t *testing.T) {
	repoDir := setupRootRepo(t, "dotfiles", "nvim", filepath.Join(t.TempDir(), "nvim"), "init.lua")
	dataDir, err := paths.Data()
	if err != nil {
		t.Fatal(err)
	}
	reg, err := manifest.ReadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	loc, err := namespace.Resolve(dataDir, reg.Repos, "nvim", "")
	if err != nil {
		t.Fatal(err)
	}
	if loc.Dir != repoDir || loc.RepoDir != repoDir || !loc.Root || !loc.Installed {
		t.Fatalf("Located = %+v", loc)
	}
	names, err := namespace.LocalNames(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(names)
	if want := []string{"nvim", "shell"}; !slices.Equal(names, want) {
		t.Fatalf("LocalNames = %v, want %v", names, want)
	}
}

func TestEnable_RootNamespaceLinksItsItems(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "nvim")
	repoDir := setupRootRepo(t, "dotfiles", "nvim", dest, "init.lua")
	captureStdoutStderr(t, func() {
		if err := enableNamespace("nvim", shared.Flags{}); err != nil {
			t.Fatalf("enable: %v", err)
		}
	})
	assertLinked(t, filepath.Join(dest, "init.lua"), filepath.Join(repoDir, "init.lua"))
	if _, err := os.Lstat(filepath.Join(dest, "shell")); !os.IsNotExist(err) {
		t.Fatal("a folder namespace must never be linked as a root item")
	}
}

func TestRootNamespace_RefusesUninstallRmAndMv(t *testing.T) {
	setupRootRepo(t, "dotfiles", "nvim", filepath.Join(t.TempDir(), "nvim"), "init.lua")
	const refusal = "root namespace"
	if err := uninstallNamespaces([]string{"nvim"}, shared.Flags{Yes: true}); err == nil || !strings.Contains(err.Error(), refusal) {
		t.Fatalf("uninstall of a root namespace must be refused, got %v", err)
	}
	if err := rmNamespaces([]string{"nvim"}, shared.Flags{Yes: true}); err == nil || !strings.Contains(err.Error(), refusal) {
		t.Fatalf("namespace rm of a root namespace must be refused, got %v", err)
	}
	if err := HandleMv([]string{"dotfiles/nvim", "dotfiles/moved"}, shared.Flags{Yes: true}); err == nil || !strings.Contains(err.Error(), refusal) {
		t.Fatalf("mv out of a root namespace must be refused, got %v", err)
	}
}

func TestRepoRm_TrashesARootNamespaceRepositoryOnce(t *testing.T) {
	repoDir := setupRootRepo(t, "dotfiles", "nvim", filepath.Join(t.TempDir(), "nvim"), "init.lua")
	captureStdoutStderr(t, func() {
		if err := rmRepo("dotfiles", shared.Flags{Yes: true, Force: true}); err != nil {
			t.Fatalf("repo rm: %v", err)
		}
	})
	if _, err := os.Stat(repoDir); !os.IsNotExist(err) {
		t.Fatalf("clone still present after repo rm: %v", err)
	}
}

func TestRn_RootNamespaceRewritesNameOnly(t *testing.T) {
	repoDir := setupRootRepo(t, "dotfiles", "nvim", filepath.Join(t.TempDir(), "nvim"), "init.lua")
	if err := renameNamespace("nvim", "editor", shared.Flags{}); err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Read(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "editor" {
		t.Fatalf("root name = %q, want editor", m.Name)
	}
	if _, err := os.Stat(filepath.Join(repoDir, "init.lua")); err != nil {
		t.Fatal("rn of the root namespace must move nothing")
	}
}

func TestAdd_RootNamespaceRefusesANamespaceFolderName(t *testing.T) {
	repoDir := setupRootRepo(t, "dotfiles", "nvim", "", "init.lua")
	src := filepath.Join(t.TempDir(), "shell")
	if err := os.WriteFile(src, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := namespace.Add(repoDir, src); err == nil {
		t.Fatal("adding an entry named like a namespace folder into the root must be refused")
	}
}

func TestCp_FromRootNamespaceCopiesOnlyTrackedItems(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "nvim")
	repoDir := setupRootRepo(t, "dotfiles", "nvim", dest, "init.lua")
	if err := HandleCp([]string{"dotfiles/nvim", "dotfiles/duplicate"}, shared.Flags{}); err != nil {
		t.Fatal(err)
	}
	copyDir := filepath.Join(repoDir, "duplicate")
	if _, err := os.Stat(filepath.Join(copyDir, "init.lua")); err != nil {
		t.Fatal("tracked item not copied")
	}
	for _, never := range []string{".git", "shell"} {
		if _, err := os.Stat(filepath.Join(copyDir, never)); !os.IsNotExist(err) {
			t.Fatalf("%s must never be copied out of a root namespace", never)
		}
	}
	m, err := manifest.Read(copyDir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "" || m.Dest != dest {
		t.Fatalf("copied manifest = %+v, want name dropped and dest kept", m)
	}
}

func TestAddNamespace_RefusesANameHeldByARootEntry(t *testing.T) {
	setupRootRepo(t, "dotfiles", "nvim", "", "init.lua")
	if err := createNamespace("init.lua", shared.Flags{}); err == nil {
		t.Fatal("a namespace folder named like a root entry must be refused")
	}
}
