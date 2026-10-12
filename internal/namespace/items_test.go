package namespace

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/DeprecatedLuar/ireallylovemydots/internal/manifest"
)

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestItems_FolderNamespaceSkipsReservedNames(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{".dots", ".gitignore", ".profiles/.dots", "init.lua", ".bashrc"} {
		touch(t, filepath.Join(dir, n))
	}
	got, err := Items(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	if want := []string{".bashrc", "init.lua"}; !slices.Equal(got, want) {
		t.Fatalf("Items = %v, want %v", got, want)
	}
}

func TestItems_RootSkipsGitAndNamespaceFolders(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{".dots", ".git/HEAD", "shell/.dots", "lua/init.lua", "README.md"} {
		touch(t, filepath.Join(dir, n))
	}
	got, err := Items(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	if want := []string{"README.md", "lua"}; !slices.Equal(got, want) {
		t.Fatalf("Items(root) = %v, want %v", got, want)
	}
}

func TestLinkable_ExpandsOnlyUnderDest(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "a"))
	touch(t, filepath.Join(dir, "b"))
	got, err := Linkable(dir, manifest.Manifest{Dest: "/d", Entries: []manifest.Entry{{Name: "a", Dest: manifest.DestNone}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1] != (manifest.Entry{Name: "b", Dest: "/d/b"}) {
		t.Fatalf("Linkable = %+v", got)
	}
}

func TestInspect_NoUntrackedUnderDest(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "loose"))
	if err := manifest.Write(dir, manifest.Manifest{Dest: "/d"}); err != nil {
		t.Fatal(err)
	}
	r, err := Inspect(dir, manifest.Manifest{Dest: "/d"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Untracked) != 0 {
		t.Fatalf("Untracked = %v, want none under a default destination", r.Untracked)
	}
}
