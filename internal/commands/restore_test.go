package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/state"
)

func TestHandleRestore_NotInstalled_ErrorsNamingInstall(t *testing.T) {
	home := t.TempDir()
	source := newCatalogueSourceRepo(t, home)
	registerClonedCatalogue(t, source)

	err := HandleRestore([]string{"editors"}, shared.Flags{})
	if err == nil {
		t.Fatal("expected restoring an uninstalled namespace to fail")
	}
	if !strings.Contains(err.Error(), "install") {
		t.Fatalf("expected the error to name install, got: %v", err)
	}
}

func TestHandleRestore_OccupiedDestination_NonInteractive_ErrorsAndChangesNothing(t *testing.T) {
	home := t.TempDir()
	source := newCatalogueSourceRepo(t, home)
	registerClonedCatalogue(t, source)

	if err := installNamespaces([]string{"editors"}, shared.Flags{}); err != nil {
		t.Fatalf("install editors: %v", err)
	}

	dest := filepath.Join(home, ".editors")
	if err := os.WriteFile(dest, []byte("occupant"), 0644); err != nil {
		t.Fatal(err)
	}

	err := HandleRestore([]string{"editors"}, shared.Flags{})
	if err == nil {
		t.Fatal("expected restore to refuse an occupied destination non-interactively without --force")
	}

	content, readErr := os.ReadFile(dest)
	if readErr != nil || string(content) != "occupant" {
		t.Fatalf("expected the occupant left untouched at %s, got content=%q err=%v", dest, content, readErr)
	}

	s, stateErr := state.Read()
	if stateErr != nil {
		t.Fatal(stateErr)
	}
	if entry, ok := s.Entries[state.Key{Repo: "dotfiles", Namespace: "editors"}]; ok && entry.Enabled {
		t.Fatalf("expected no state change from a refused restore, got %+v", entry)
	}
}

func TestHandleRestore_Force_TrashesOccupantAndRestores(t *testing.T) {
	home := t.TempDir()
	source := newCatalogueSourceRepo(t, home)
	registerClonedCatalogue(t, source)

	if err := installNamespaces([]string{"editors"}, shared.Flags{}); err != nil {
		t.Fatalf("install editors: %v", err)
	}

	dest := filepath.Join(home, ".editors")
	if err := os.WriteFile(dest, []byte("occupant"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := HandleRestore([]string{"editors"}, shared.Flags{Force: true}); err != nil {
		t.Fatalf("restore --force: %v", err)
	}

	info, err := os.Lstat(dest)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected a real directory (our restored payload) at %s, got err=%v", dest, err)
	}
	if !info.IsDir() {
		t.Fatalf("expected %s to be a directory, matching the payload", dest)
	}
	content, err := os.ReadFile(filepath.Join(dest, "seed"))
	if err != nil || string(content) != "x" {
		t.Fatalf("expected the payload's content at %s, got %q err=%v", dest, content, err)
	}

	s, err := state.Read()
	if err != nil {
		t.Fatal(err)
	}
	entry := s.Entries[state.Key{Repo: "dotfiles", Namespace: "editors"}]
	if entry.Enabled {
		t.Fatal("expected the namespace recorded disabled after restore")
	}
	if len(entry.LinkedDests) != 0 {
		t.Fatalf("expected no linked destinations recorded, got %+v", entry.LinkedDests)
	}
}
