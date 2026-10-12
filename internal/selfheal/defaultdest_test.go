package selfheal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DeprecatedLuar/ireallylovemydots/internal/manifest"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/state"
)

// destNamespace turns setupNamespace's enabled namespace into one with a
// default destination covering its "cfg" item, already linked.
func destNamespace(t *testing.T) (namespaceDir, destDir string) {
	t.Helper()
	destDir = filepath.Join(t.TempDir(), ".config", "app")
	namespaceDir, payload := setupNamespace(t, filepath.Join(destDir, "cfg"))
	mustSymlink(t, payload, filepath.Join(destDir, "cfg"))
	if err := manifest.Write(namespaceDir, manifest.Manifest{Dest: destDir}); err != nil {
		t.Fatal(err)
	}
	return namespaceDir, destDir
}

// A new item under a default destination is a missing link: self-heal
// links it.
func TestRun_LinksNewItemUnderDefaultDestination(t *testing.T) {
	namespaceDir, destDir := destNamespace(t)
	added := filepath.Join(namespaceDir, "new.conf")
	if err := os.WriteFile(added, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := readLink(t, filepath.Join(destDir, "new.conf")); got != added {
		t.Fatalf("new.conf -> %s, want %s", got, added)
	}
	s, err := state.Read()
	if err != nil {
		t.Fatal(err)
	}
	entry := s.Entries[state.Key{Repo: "dotfiles", Namespace: "cfg-ns"}]
	if !entry.Enabled || len(entry.LinkedDests) != 2 {
		t.Fatalf("state = %+v, want enabled with both destinations linked", entry)
	}
}

// An occupied target for a new item disables the namespace and leaves the
// occupant alone.
func TestRun_OccupiedNewItemDisablesNamespace(t *testing.T) {
	namespaceDir, destDir := destNamespace(t)
	if err := os.WriteFile(filepath.Join(namespaceDir, "new.conf"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	occupant := filepath.Join(destDir, "new.conf")
	if err := os.WriteFile(occupant, []byte("mine"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	s, err := state.Read()
	if err != nil {
		t.Fatal(err)
	}
	if s.Entries[state.Key{Repo: "dotfiles", Namespace: "cfg-ns"}].Enabled {
		t.Fatal("namespace still enabled with an occupied destination")
	}
	if got, err := os.ReadFile(occupant); err != nil || string(got) != "mine" {
		t.Fatalf("occupant = %q, %v; must be untouched", got, err)
	}
	if _, err := os.Lstat(filepath.Join(destDir, "cfg")); !os.IsNotExist(err) {
		t.Fatalf("cfg link still present after disable: %v", err)
	}
}
