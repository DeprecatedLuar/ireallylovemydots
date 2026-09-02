package profile

import (
	"os"
	"path/filepath"
	"testing"
)

// namespaceWithEntries builds a namespace folder holding one file entry and
// one directory entry, both declared profiled.
func namespaceWithEntries(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "gitconfig"), []byte("root"), 0644); err != nil {
		t.Fatal(err)
	}
	tree := filepath.Join(dir, "nvim")
	if err := os.MkdirAll(filepath.Join(tree, "lua"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "lua", "init.lua"), []byte("root tree"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, Manifest{Entries: []string{"gitconfig", "nvim"}}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestReadMissingFileIsEmpty(t *testing.T) {
	m, err := Read(t.TempDir())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(m.Profiles) != 0 || len(m.Entries) != 0 {
		t.Fatalf("expected an empty manifest, got %+v", m)
	}
}

func TestWriteSortsBothArrays(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, Manifest{Profiles: []string{"light", "dark"}, Entries: []string{"kitty.conf", "gitconfig"}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	m, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if m.Profiles[0] != "dark" || m.Profiles[1] != "light" {
		t.Fatalf("profiles not sorted: %v", m.Profiles)
	}
	if m.Entries[0] != "gitconfig" || m.Entries[1] != "kitty.conf" {
		t.Fatalf("entries not sorted: %v", m.Entries)
	}
}

func TestSourceResolvesRootAndOverride(t *testing.T) {
	dir := namespaceWithEntries(t)
	if err := Create(dir, "dark", ""); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := AddOverride(dir, "dark", "gitconfig"); err != nil {
		t.Fatalf("AddOverride: %v", err)
	}

	cases := []struct {
		name, entry, active, want string
	}{
		{"main is the root", "gitconfig", "", filepath.Join(dir, "gitconfig")},
		{"main by name is the root", "gitconfig", Main, filepath.Join(dir, "gitconfig")},
		{"overridden entry", "gitconfig", "dark", filepath.Join(ProfileDir(dir, "dark"), "gitconfig")},
		{"entry the profile does not override", "nvim", "dark", filepath.Join(dir, "nvim")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Source(dir, c.entry, c.active)
			if err != nil {
				t.Fatalf("Source: %v", err)
			}
			if got != c.want {
				t.Fatalf("Source = %q, want %q", got, c.want)
			}
		})
	}
}

// An override file present for an entry nobody declared profiled is drift
// self-heal warns about; resolution ignores it rather than silently linking
// a file no manifest names.
func TestSourceIgnoresUndeclaredOverride(t *testing.T) {
	dir := namespaceWithEntries(t)
	if err := Create(dir, "dark", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ProfileDir(dir, "dark"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ProfileDir(dir, "dark"), "stray"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := Source(dir, "stray", "dark")
	if err != nil {
		t.Fatalf("Source: %v", err)
	}
	if got != filepath.Join(dir, "stray") {
		t.Fatalf("Source = %q, want the namespace root path", got)
	}
}

func TestAddOverrideCopiesDirectoryWhole(t *testing.T) {
	dir := namespaceWithEntries(t)
	if err := Create(dir, "dark", ""); err != nil {
		t.Fatal(err)
	}
	if err := AddOverride(dir, "dark", "nvim"); err != nil {
		t.Fatalf("AddOverride: %v", err)
	}

	copied := filepath.Join(ProfileDir(dir, "dark"), "nvim", "lua", "init.lua")
	data, err := os.ReadFile(copied)
	if err != nil {
		t.Fatalf("read copied tree: %v", err)
	}
	if string(data) != "root tree" {
		t.Fatalf("copied content = %q", data)
	}
}

func TestCreateFromMainSeedsEveryProfiledEntry(t *testing.T) {
	dir := namespaceWithEntries(t)
	if err := Create(dir, "dark", Main); err != nil {
		t.Fatalf("Create: %v", err)
	}

	overrides, err := Overrides(dir, "dark")
	if err != nil {
		t.Fatal(err)
	}
	if len(overrides) != 2 || overrides[0] != "gitconfig" || overrides[1] != "nvim" {
		t.Fatalf("overrides = %v, want both profiled entries", overrides)
	}
}

func TestCreateFromProfileCopiesThatProfilesVersion(t *testing.T) {
	dir := namespaceWithEntries(t)
	if err := Create(dir, "dark", ""); err != nil {
		t.Fatal(err)
	}
	if err := AddOverride(dir, "dark", "gitconfig"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ProfileDir(dir, "dark"), "gitconfig"), []byte("dark"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := Create(dir, "darker", "dark"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(ProfileDir(dir, "darker"), "gitconfig"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "dark" {
		t.Fatalf("seeded content = %q, want dark's version", data)
	}
}

func TestRemoveDropsDeclarationAndTrashesOverrides(t *testing.T) {
	dir := namespaceWithEntries(t)
	if err := Create(dir, "dark", Main); err != nil {
		t.Fatal(err)
	}
	if err := Remove(dir, "dark"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	m, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.HasProfile("dark") {
		t.Fatal("dark is still declared")
	}
	if _, err := os.Lstat(ProfileDir(dir, "dark")); !os.IsNotExist(err) {
		t.Fatalf("expected the profile folder to be gone, got err=%v", err)
	}
}

func TestRenameMovesFolderAndDeclaration(t *testing.T) {
	dir := namespaceWithEntries(t)
	if err := Create(dir, "dark", Main); err != nil {
		t.Fatal(err)
	}
	if err := Rename(dir, "dark", "night"); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	m, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.HasProfile("dark") || !m.HasProfile("night") {
		t.Fatalf("profiles = %v", m.Profiles)
	}
	if _, err := os.Lstat(filepath.Join(ProfileDir(dir, "night"), "gitconfig")); err != nil {
		t.Fatalf("renamed folder does not hold the override: %v", err)
	}
}

func TestOverridingProfilesNamesEveryHolder(t *testing.T) {
	dir := namespaceWithEntries(t)
	for _, name := range []string{"dark", "light"} {
		if err := Create(dir, name, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := AddOverride(dir, "dark", "gitconfig"); err != nil {
		t.Fatal(err)
	}
	if err := AddOverride(dir, "light", "gitconfig"); err != nil {
		t.Fatal(err)
	}

	holding, err := OverridingProfiles(dir, "gitconfig")
	if err != nil {
		t.Fatal(err)
	}
	if len(holding) != 2 || holding[0] != "dark" || holding[1] != "light" {
		t.Fatalf("holding = %v", holding)
	}
}
