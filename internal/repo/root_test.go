package repo

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/DeprecatedLuar/ireallylovemydots/internal/manifest"
)

func writeRootDots(t *testing.T, dir string, m manifest.Manifest) {
	t.Helper()
	if err := manifest.Write(dir, m); err != nil {
		t.Fatal(err)
	}
}

func TestRootName(t *testing.T) {
	dir := t.TempDir()
	if name, err := RootName(dir); err != nil || name != "" {
		t.Fatalf("no root .dots: RootName = %q, %v", name, err)
	}
	writeRootDots(t, dir, manifest.Manifest{Name: "nvim"})
	if name, err := RootName(dir); err != nil || name != "nvim" {
		t.Fatalf("RootName = %q, %v", name, err)
	}
	writeRootDots(t, dir, manifest.Manifest{Name: "nvim", Ignore: true})
	if name, err := RootName(dir); err != nil || name != "" {
		t.Fatalf("ignored root: RootName = %q, %v", name, err)
	}
	writeRootDots(t, dir, manifest.Manifest{})
	if _, err := RootName(dir); err == nil {
		t.Fatal("root .dots without name must be an error")
	}
}

func TestNamespaceDirAndOwnerOf(t *testing.T) {
	dir := t.TempDir()
	writeRootDots(t, dir, manifest.Manifest{Name: "nvim"})
	if err := os.MkdirAll(filepath.Join(dir, "shell"), 0755); err != nil {
		t.Fatal(err)
	}
	writeRootDots(t, filepath.Join(dir, "shell"), manifest.Manifest{})

	if got, _ := NamespaceDir(dir, "nvim"); got != dir {
		t.Fatalf("NamespaceDir(root) = %s", got)
	}
	if got, _ := NamespaceDir(dir, "shell"); got != filepath.Join(dir, "shell") {
		t.Fatalf("NamespaceDir(shell) = %s", got)
	}
	if got, _ := OwnerOf(dir, "lua"); got != "nvim" {
		t.Fatalf("OwnerOf(lua) = %s, want nvim", got)
	}
	if got, _ := OwnerOf(dir, "shell"); got != "shell" {
		t.Fatalf("OwnerOf(shell) = %s, want shell", got)
	}
}

func TestNamespaces_IncludesRoot(t *testing.T) {
	dir := initGitRepo(t)
	writeRootDots(t, dir, manifest.Manifest{Name: "nvim"})
	if err := os.WriteFile(filepath.Join(dir, "init.lua"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, dir)
	got, err := Namespaces(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got, "nvim") {
		t.Fatalf("Namespaces = %v, want it to include the root namespace", got)
	}
}
