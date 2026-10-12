package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DeprecatedLuar/ireallylovemydots/internal/commands/shared"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/manifest"
)

// destFixture gives the registered "editors" namespace a default
// destination and two items with no entries.
func destFixture(t *testing.T) (nsName, nsDir, dest string) {
	t.Helper()
	nsName = setupRegisteredNamespace(t)
	nsDir = filepath.Join(os.Getenv("XDG_DATA_HOME"), "ireallylovemydots", "dotfiles", nsName)
	dest = filepath.Join(t.TempDir(), "nvim")
	for _, n := range []string{"init.lua", "README.md"} {
		if err := os.WriteFile(filepath.Join(nsDir, n), []byte(n), 0644); err != nil {
			t.Fatal(err)
		}
	}
	m := manifest.Manifest{Dest: dest, Entries: []manifest.Entry{{Name: "README.md", Dest: manifest.DestNone}}}
	if err := manifest.Write(nsDir, m); err != nil {
		t.Fatal(err)
	}
	return nsName, nsDir, dest
}

func TestEnable_LinksItemsUnderDefaultDestination(t *testing.T) {
	nsName, nsDir, dest := destFixture(t)
	captureStdoutStderr(t, func() {
		if err := enableNamespace(nsName, shared.Flags{}); err != nil {
			t.Fatalf("enable: %v", err)
		}
	})
	assertLinked(t, filepath.Join(dest, "init.lua"), filepath.Join(nsDir, "init.lua"))
	if _, err := os.Lstat(filepath.Join(dest, "README.md")); !os.IsNotExist(err) {
		t.Fatalf("README.md overridden to none must not be linked, lstat err = %v", err)
	}
}
