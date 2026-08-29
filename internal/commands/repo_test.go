package commands

import (
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/manifest"
)

func TestAddRepo_RejectsReservedDerivedName(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	if err := addRepo("https://example.com/owner/list.git"); err == nil {
		t.Fatal("expected error registering a repository named \"list\"")
	}

	reg, err := manifest.ReadRegistry()
	if err != nil {
		t.Fatalf("ReadRegistry: %v", err)
	}
	if len(reg.Repos) != 0 {
		t.Fatalf("registry should be untouched, got %+v", reg.Repos)
	}
}

func TestAddRepo_RejectsDuplicateDerivedName_NonInteractive(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	existing := manifest.Registry{Repos: []manifest.Repo{
		{Name: "dotfiles", Owner: "someone", URL: "https://example.com/someone/dotfiles"},
	}}
	if err := manifest.WriteRegistry(existing); err != nil {
		t.Fatalf("WriteRegistry: %v", err)
	}

	if err := addRepo("https://example.com/other/dotfiles.git"); err == nil {
		t.Fatal("expected error registering a duplicate name outside a terminal")
	}

	reg, err := manifest.ReadRegistry()
	if err != nil {
		t.Fatalf("ReadRegistry: %v", err)
	}
	if len(reg.Repos) != 1 {
		t.Fatalf("registry should be untouched, got %+v", reg.Repos)
	}
}
