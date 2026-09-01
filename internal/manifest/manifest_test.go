package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
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

func TestWrite_IsValidTOML(t *testing.T) {
	dir := t.TempDir()
	m := Manifest{Entries: []Entry{
		{Name: "nvim", Dest: "~/.config/nvim"},
		{Name: "kitty", Dest: "~/.config/kitty"},
	}}
	if err := Write(dir, m); err != nil {
		t.Fatalf("Write error: %v", err)
	}

	raw, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "[[entries]]") {
		t.Fatalf("expected TOML array-of-tables syntax, got: %s", raw)
	}

	var decoded Manifest
	if _, err := toml.Decode(string(raw), &decoded); err != nil {
		t.Fatalf("on-disk manifest is not valid TOML: %v", err)
	}
}

func TestRoundTrip_ByteIdentical(t *testing.T) {
	dir := t.TempDir()
	m := Manifest{Entries: []Entry{
		{Name: "zsh", Dest: "~/.zshrc"},
		{Name: "kitty", Dest: "~/.config/kitty"},
		{Name: "nvim", Dest: "~/.config/nvim"},
	}}
	if err := Write(dir, m); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	first, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}

	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if err := Write(dir, got); err != nil {
		t.Fatalf("Write (second) error: %v", err)
	}
	second, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}

	if string(first) != string(second) {
		t.Fatalf("manifest did not round-trip byte-identically:\nfirst:  %s\nsecond: %s", first, second)
	}
}

func TestRoundTrip_PreservesIgnore(t *testing.T) {
	dir := t.TempDir()
	m := Manifest{Ignore: true, Entries: []Entry{
		{Name: "nvim", Dest: "~/.config/nvim"},
	}}
	if err := Write(dir, m); err != nil {
		t.Fatalf("Write error: %v", err)
	}

	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if !got.Ignore {
		t.Fatalf("Ignore did not round-trip: got %+v", got)
	}

	raw, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "ignore = true") {
		t.Fatalf("expected on-disk manifest to carry ignore = true, got: %s", raw)
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
