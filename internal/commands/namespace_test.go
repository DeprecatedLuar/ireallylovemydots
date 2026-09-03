package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/engine"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/paths"
	"github.com/DeprecatedLuar/dotz/internal/state"
)

func TestHandleNamespace_AddRejectsReservedName(t *testing.T) {
	err := HandleNamespace([]string{"add", "list"}, shared.Flags{})
	if err == nil {
		t.Fatal("expected error creating a namespace named \"list\"")
	}
}

func TestHandleNamespace_MvRejectsReservedTarget(t *testing.T) {
	err := HandleNamespace([]string{"mv", "neovim", "sync"}, shared.Flags{})
	if err == nil {
		t.Fatal("expected error renaming a namespace to a reserved name")
	}
}

// setupRegisteredNamespace registers one repository with one materialized,
// empty namespace, returning its name for use in grammar-flip tests.
func setupRegisteredNamespace(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	reg := manifest.Registry{Repos: []manifest.Repo{
		{Name: "dotfiles", Owner: "someone", URL: "https://example.com/someone/dotfiles"},
	}}
	if err := manifest.WriteRegistry(reg); err != nil {
		t.Fatalf("WriteRegistry: %v", err)
	}
	repoDir := filepath.Join(dataHome, "ireallylovemydots", "dotfiles")
	nsDir := filepath.Join(repoDir, "editors")
	if err := os.MkdirAll(nsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Write(nsDir, manifest.Manifest{}); err != nil {
		t.Fatal(err)
	}
	return "editors"
}

func TestHandleNamespace_EditBothSpellingsReachSameHandler(t *testing.T) {
	name := setupRegisteredNamespace(t)
	t.Setenv("EDITOR", "")

	err1 := HandleNamespace([]string{"edit", name}, shared.Flags{})
	err2 := HandleNamespace([]string{name, "edit"}, shared.Flags{})
	if err1 == nil || err2 == nil {
		t.Fatalf("expected both spellings to fail identically with no $EDITOR set, got %v / %v", err1, err2)
	}
	if err1.Error() != err2.Error() {
		t.Fatalf("namespace edit %s vs namespace %s edit reached different handlers: %v vs %v", name, name, err1, err2)
	}
}

func TestHandleNamespace_EnableBothSpellingsReachSameHandler(t *testing.T) {
	name := setupRegisteredNamespace(t)

	// Both calls enable the same empty namespace (no manifest entries, so
	// no pre-flight problems); what matters is that both spellings reach
	// the same handler and behave identically, not that enabling twice is
	// itself interesting.
	err1 := HandleNamespace([]string{"enable", name}, shared.Flags{})
	err2 := HandleNamespace([]string{name, "enable"}, shared.Flags{})
	if err1 != nil || err2 != nil {
		t.Fatalf("expected both spellings to enable an empty namespace without error, got %v / %v", err1, err2)
	}
}

func TestHandleNamespace_DisableBothSpellingsReachSameHandler(t *testing.T) {
	name := setupRegisteredNamespace(t)

	// Neither call has anything enabled to disable; both spellings must
	// still reach the same handler and behave identically (a no-op).
	err1 := HandleNamespace([]string{"disable", name}, shared.Flags{})
	err2 := HandleNamespace([]string{name, "disable"}, shared.Flags{})
	if err1 != nil || err2 != nil {
		t.Fatalf("expected both spellings to no-op disabling an unenabled namespace, got %v / %v", err1, err2)
	}
}

// TestRenameNamespace_RelinksLiveSymlink is the regression test for the bug
// report: every symlink dots creates targets an absolute path built from the
// namespace's own name, so renaming an enabled namespace must repoint its
// destination's symlink as part of the rename, not leave it dangling for the
// next self-heal pass to (maybe) catch before some other program notices the
// destination is gone and recreates it as a real file.
func TestRenameNamespace_RelinksLiveSymlink(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	home := t.TempDir()

	reg := manifest.Registry{Repos: []manifest.Repo{{Name: "dotfiles"}}}
	if err := manifest.WriteRegistry(reg); err != nil {
		t.Fatal(err)
	}
	dataDir, err := paths.Data()
	if err != nil {
		t.Fatal(err)
	}
	repoDir := filepath.Join(dataDir, "dotfiles")
	nsDir := filepath.Join(repoDir, "editors")
	if err := os.MkdirAll(nsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nsDir, "cfg"), []byte("payload"), 0644); err != nil {
		t.Fatal(err)
	}
	entries := []manifest.Entry{{Name: "cfg", Dest: filepath.Join(home, "cfg")}}
	if err := manifest.Write(nsDir, manifest.Manifest{Entries: entries}); err != nil {
		t.Fatal(err)
	}

	key := state.Key{Repo: "dotfiles", Namespace: "editors"}
	s := state.State{Entries: map[state.Key]state.Entry{}}
	if _, err := engine.Enable(key, repoDir, nsDir, "editors", entries, s, nil); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	dest := entries[0].Dest
	before, err := os.Readlink(dest)
	if err != nil {
		t.Fatal(err)
	}
	if before != filepath.Join(nsDir, "cfg") {
		t.Fatalf("precondition: dest = %q, want it linked into the pre-rename namespace dir", before)
	}

	if err := renameNamespace("editors", "tools", shared.Flags{}); err != nil {
		t.Fatalf("renameNamespace: %v", err)
	}

	newNsDir := filepath.Join(repoDir, "tools")
	if _, err := os.Stat(newNsDir); err != nil {
		t.Fatalf("expected renamed namespace folder: %v", err)
	}

	after, err := os.Readlink(dest)
	if err != nil {
		t.Fatalf("readlink %s after rename: %v", dest, err)
	}
	if after != filepath.Join(newNsDir, "cfg") {
		t.Fatalf("dest still targets %q after rename, want it repointed into %s", after, newNsDir)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read %s through its (supposedly repointed) symlink: %v", dest, err)
	}
	if string(got) != "payload" {
		t.Fatalf("content through dest = %q, want payload", got)
	}

	persisted, err := state.Read()
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := persisted.Entries[state.Key{Repo: "dotfiles", Namespace: "tools"}]
	if !ok || !entry.Enabled {
		t.Fatalf("expected state moved under the new namespace name and still enabled, got %+v ok=%v", entry, ok)
	}
	if len(entry.LinkedDests) != 1 || entry.LinkedDests[0] != dest {
		t.Fatalf("LinkedDests = %v, want [%s]", entry.LinkedDests, dest)
	}
}

func TestPrepareEditBuffer_PrePopulatesUntrackedPayload(t *testing.T) {
	nsDir := t.TempDir()
	if err := manifest.Write(nsDir, manifest.Manifest{Entries: []manifest.Entry{
		{Name: "nvim", Dest: "~/.config/nvim"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nsDir, "starship.toml"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	buf, err := prepareEditBuffer(nsDir)
	if err != nil {
		t.Fatalf("prepareEditBuffer: %v", err)
	}

	m, err := manifest.Decode(buf)
	if err != nil {
		t.Fatalf("Decode buffer: %v", err)
	}
	if len(m.Entries) != 2 {
		t.Fatalf("expected 2 entries (1 tracked + 1 untracked placeholder), got %+v", m.Entries)
	}
	var found bool
	for _, e := range m.Entries {
		if e.Name == "starship.toml" {
			found = true
			if e.Dest != "" {
				t.Fatalf("expected empty destination placeholder, got %q", e.Dest)
			}
		}
	}
	if !found {
		t.Fatal("expected a placeholder entry for the untracked payload")
	}
}

func TestEditNamespace_QuittingWithoutSavingLeavesManifestByteIdentical(t *testing.T) {
	nsDir := t.TempDir()
	if err := manifest.Write(nsDir, manifest.Manifest{Entries: []manifest.Entry{
		{Name: "nvim", Dest: "~/.config/nvim"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nsDir, "starship.toml"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadFile(manifest.Path(nsDir))
	if err != nil {
		t.Fatal(err)
	}

	buf, err := prepareEditBuffer(nsDir)
	if err != nil {
		t.Fatalf("prepareEditBuffer: %v", err)
	}
	// The editor "quits without saving": applyEditedBuffer sees the exact
	// bytes it seeded the buffer with.
	if err := applyEditedBuffer(nsDir, buf, buf); err != nil {
		t.Fatalf("applyEditedBuffer: %v", err)
	}

	after, err := os.ReadFile(manifest.Path(nsDir))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("manifest changed after a no-op edit:\nbefore: %s\nafter:  %s", before, after)
	}
}

func TestEditNamespace_SavingPersistsChanges(t *testing.T) {
	nsDir := t.TempDir()
	if err := manifest.Write(nsDir, manifest.Manifest{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nsDir, "starship.toml"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	buf, err := prepareEditBuffer(nsDir)
	if err != nil {
		t.Fatalf("prepareEditBuffer: %v", err)
	}
	m, err := manifest.Decode(buf)
	if err != nil {
		t.Fatal(err)
	}
	for i := range m.Entries {
		if m.Entries[i].Name == "starship.toml" {
			m.Entries[i].Dest = "~/.config/starship.toml"
		}
	}
	edited, err := manifest.Encode(m)
	if err != nil {
		t.Fatal(err)
	}

	if err := applyEditedBuffer(nsDir, buf, edited); err != nil {
		t.Fatalf("applyEditedBuffer: %v", err)
	}

	got, err := manifest.Read(nsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 1 || got.Entries[0].Name != "starship.toml" {
		t.Fatalf("expected the filled-in entry to persist, got %+v", got.Entries)
	}
}
