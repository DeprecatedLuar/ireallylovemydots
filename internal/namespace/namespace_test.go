package namespace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/manifest"
)

func TestCreate(t *testing.T) {
	repoDir := t.TempDir()

	dir, err := Create(repoDir, "editors")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := os.Stat(manifest.Path(dir)); err != nil {
		t.Fatalf("expected manifest at %s: %v", manifest.Path(dir), err)
	}

	if _, err := Create(repoDir, "editors"); err == nil {
		t.Fatal("expected error creating a namespace that already exists")
	}
}

func TestLocalNames(t *testing.T) {
	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(repoDir, "editors"); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(repoDir, "shell"); err != nil {
		t.Fatal(err)
	}

	names, err := LocalNames(repoDir)
	if err != nil {
		t.Fatalf("LocalNames: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("LocalNames = %v, want 2 entries excluding .git", names)
	}
}

func TestResolve_SingleMatch(t *testing.T) {
	dataDir := t.TempDir()
	repos := []manifest.Repo{{Name: "dotfiles"}}
	if _, err := Create(filepath.Join(dataDir, "dotfiles"), "editors"); err != nil {
		t.Fatal(err)
	}

	loc, err := Resolve(dataDir, repos, "editors", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if loc.Repo.Name != "dotfiles" {
		t.Fatalf("Resolve repo = %q, want dotfiles", loc.Repo.Name)
	}
}

func TestResolve_NotFound(t *testing.T) {
	dataDir := t.TempDir()
	repos := []manifest.Repo{{Name: "dotfiles"}}

	if _, err := Resolve(dataDir, repos, "missing", ""); err == nil {
		t.Fatal("expected error resolving a namespace that does not exist")
	}
}

func TestResolve_AmbiguousNonInteractiveErrors(t *testing.T) {
	dataDir := t.TempDir()
	repos := []manifest.Repo{{Name: "one"}, {Name: "two"}}
	if _, err := Create(filepath.Join(dataDir, "one"), "editors"); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(filepath.Join(dataDir, "two"), "editors"); err != nil {
		t.Fatal(err)
	}

	if _, err := Resolve(dataDir, repos, "editors", ""); err == nil {
		t.Fatal("expected error for an ambiguous namespace name with no --repo and no terminal")
	}
}

func TestResolve_RepoFlagDisambiguates(t *testing.T) {
	dataDir := t.TempDir()
	repos := []manifest.Repo{{Name: "one"}, {Name: "two"}}
	if _, err := Create(filepath.Join(dataDir, "one"), "editors"); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(filepath.Join(dataDir, "two"), "editors"); err != nil {
		t.Fatal(err)
	}

	loc, err := Resolve(dataDir, repos, "editors", "two")
	if err != nil {
		t.Fatalf("Resolve with --repo: %v", err)
	}
	if loc.Repo.Name != "two" {
		t.Fatalf("Resolve repo = %q, want two", loc.Repo.Name)
	}
}

func TestRename(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repoDir := t.TempDir()
	if _, err := Create(repoDir, "editors"); err != nil {
		t.Fatal(err)
	}

	if err := Rename(repoDir, "dotfiles", "editors", "tools"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoDir, "editors")); !os.IsNotExist(err) {
		t.Fatalf("expected old namespace folder gone, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(repoDir, "tools")); err != nil {
		t.Fatalf("expected renamed namespace folder: %v", err)
	}
}

func TestRename_TargetAlreadyExists(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repoDir := t.TempDir()
	if _, err := Create(repoDir, "editors"); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(repoDir, "tools"); err != nil {
		t.Fatal(err)
	}

	if err := Rename(repoDir, "dotfiles", "editors", "tools"); err == nil {
		t.Fatal("expected error renaming onto an existing namespace name")
	}
}

func TestDelete(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	repoDir := t.TempDir()
	dir, err := Create(repoDir, "editors")
	if err != nil {
		t.Fatal(err)
	}

	if err := Delete(dir); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("expected namespace folder gone, got err=%v", err)
	}
}
