package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoundTrip_SortedRegardlessOfInsertionOrder(t *testing.T) {
	dir := t.TempDir()

	m := Manifest{Entries: []Entry{
		{Name: "zsh", Dest: "~/.zshrc"},
		{Name: "kitty", Dest: "~/.config/kitty"},
		{Name: "nvim", Dest: "~/.config/nvim"},
	}}

	if err := Write(dir, m); err != nil {
		t.Fatalf("Write error: %v", err)
	}

	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}

	want := []string{"kitty", "nvim", "zsh"}
	if len(got.Entries) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got.Entries), len(want))
	}
	for i, name := range want {
		if got.Entries[i].Name != name {
			t.Errorf("entry %d: got name %q, want %q", i, got.Entries[i].Name, name)
		}
	}
}

func TestWrite_ContractsHomeOnDisk(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	m := Manifest{Entries: []Entry{
		{Name: "nvim", Dest: filepath.Join(home, ".config", "nvim")},
	}}
	if err := Write(dir, m); err != nil {
		t.Fatalf("Write error: %v", err)
	}

	raw, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "~/.config/nvim") {
		t.Fatalf("expected on-disk manifest to contract home, got: %s", raw)
	}
}

func TestRead_ExpandsHome(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	m := Manifest{Entries: []Entry{
		{Name: "nvim", Dest: "~/.config/nvim"},
	}}
	if err := Write(dir, m); err != nil {
		t.Fatalf("Write error: %v", err)
	}

	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	want := filepath.Join(home, ".config", "nvim")
	if got.Entries[0].Dest != want {
		t.Fatalf("got dest %q, want %q", got.Entries[0].Dest, want)
	}
}
