package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initGitRepo creates an empty git repository at a fresh temp dir, with no
// commits yet unless files are added and committed by the caller.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	return dir
}

func commitAll(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("add", ".")
	run("commit", "-m", "test")
}

func TestRootEntries_EmptyRepository(t *testing.T) {
	dir := initGitRepo(t)

	entries, err := RootEntries(dir)
	if err != nil {
		t.Fatalf("RootEntries error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no root entries for an uncommitted repository, got %+v", entries)
	}
}

func TestRootEntries_NamespacesAndPlainFiles(t *testing.T) {
	dir := initGitRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, "editors"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "editors", ".dots"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, dir)

	entries, err := RootEntries(dir)
	if err != nil {
		t.Fatalf("RootEntries error: %v", err)
	}

	byName := map[string]RootEntry{}
	for _, e := range entries {
		byName[e.Name] = e
	}

	editors, ok := byName["editors"]
	if !ok || !editors.IsDir || !editors.HasDots {
		t.Fatalf("expected editors to be a directory holding .dots, got %+v (ok=%v)", editors, ok)
	}
	readme, ok := byName["README.md"]
	if !ok || readme.IsDir || readme.HasDots {
		t.Fatalf("expected README.md to be a plain file, got %+v (ok=%v)", readme, ok)
	}
}

func TestRootEntries_PlainSourceFolders(t *testing.T) {
	dir := initGitRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, dir)

	entries, err := RootEntries(dir)
	if err != nil {
		t.Fatalf("RootEntries error: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "src" || !entries[0].IsDir || entries[0].HasDots {
		t.Fatalf("expected one plain directory entry with no .dots, got %+v", entries)
	}
}

func TestNamespaces_FiltersOutFoldersWithoutDots(t *testing.T) {
	dir := initGitRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, "editors"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "editors", ".dots"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "notanamespace"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notanamespace", "file.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, dir)

	names, err := Namespaces(dir)
	if err != nil {
		t.Fatalf("Namespaces error: %v", err)
	}
	if len(names) != 1 || names[0] != "editors" {
		t.Fatalf("Namespaces = %v, want [editors]", names)
	}
}

func TestDiskEntries_MatchesRootEntriesShape(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "editors"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "editors", ".dots"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}

	entries, err := DiskEntries(dir)
	if err != nil {
		t.Fatalf("DiskEntries error: %v", err)
	}

	byName := map[string]RootEntry{}
	for _, e := range entries {
		byName[e.Name] = e
	}
	if e := byName["editors"]; !e.IsDir || !e.HasDots {
		t.Fatalf("expected editors to be a directory holding .dots, got %+v", e)
	}
	if e := byName["README.md"]; e.IsDir || e.HasDots {
		t.Fatalf("expected README.md to be a plain file, got %+v", e)
	}
}

func TestDiskEntries_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()

	entries, err := DiskEntries(dir)
	if err != nil {
		t.Fatalf("DiskEntries error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no entries for an empty directory, got %+v", entries)
	}
}
