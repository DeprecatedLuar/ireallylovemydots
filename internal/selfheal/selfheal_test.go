package selfheal

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/state"
)

// setupNamespace registers one repository with one namespace holding a
// single entry named "cfg", writes its manifest, materializes its payload
// on disk, and records it enabled in state with dest already linked. It
// returns the namespace directory, the entry's destination, and the
// payload path the destination should point at.
func setupNamespace(t *testing.T, dest string) (namespaceDir, payload string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	reg := manifest.Registry{Repos: []manifest.Repo{{Name: "dotfiles"}}}
	if err := manifest.WriteRegistry(reg); err != nil {
		t.Fatal(err)
	}

	namespaceDir = filepath.Join(dataHome, "ireallylovemydots", "dotfiles", "cfg-ns")
	if err := os.MkdirAll(namespaceDir, 0755); err != nil {
		t.Fatal(err)
	}
	payload = filepath.Join(namespaceDir, "cfg")
	if err := os.WriteFile(payload, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	m := manifest.Manifest{Entries: []manifest.Entry{{Name: "cfg", Dest: dest}}}
	if err := manifest.Write(namespaceDir, m); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(state.State{Entries: map[state.Key]state.Entry{
		{Repo: "dotfiles", Namespace: "cfg-ns"}: {Enabled: true, LinkedDests: []string{dest}},
	}}); err != nil {
		t.Fatal(err)
	}
	return namespaceDir, payload
}

func mustSymlink(t *testing.T, target, dest string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, dest); err != nil {
		t.Fatal(err)
	}
}

func readLink(t *testing.T, path string) string {
	t.Helper()
	target, err := os.Readlink(path)
	if err != nil {
		t.Fatalf("readlink %s: %v", path, err)
	}
	return target
}

// TestRun_RecreatesDeletedSymlink covers: deleting a symlink by hand and
// running any command recreates it silently.
func TestRun_RecreatesDeletedSymlink(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".config", "cfg")
	namespaceDir, payload := setupNamespace(t, dest)
	mustSymlink(t, payload, dest)

	if err := os.Remove(dest); err != nil {
		t.Fatal(err)
	}

	problems, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("expected no problems, got %+v", problems)
	}
	if got := readLink(t, dest); got != payload {
		t.Fatalf("expected %s to be recreated pointing at %s, got %s", dest, payload, got)
	}
	_ = namespaceDir
}

// TestRun_RepointsWrongSymlink covers: repointing a symlink by hand and
// running any command repoints it back.
func TestRun_RepointsWrongSymlink(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".config", "cfg")
	_, payload := setupNamespace(t, dest)
	mustSymlink(t, payload, dest)

	wrongTarget := filepath.Join(home, "elsewhere")
	if err := os.WriteFile(wrongTarget, []byte("y"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dest); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(wrongTarget, dest); err != nil {
		t.Fatal(err)
	}

	problems, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("expected no problems, got %+v", problems)
	}
	if got := readLink(t, dest); got != payload {
		t.Fatalf("expected %s repointed at %s, got %s", dest, payload, got)
	}
}

// TestRun_RealFileLeftAloneAndReported covers: replacing a symlink with a
// real file marks the entry a problem and is left alone by self-heal —
// concept.md "Self-healing" never destroys.
func TestRun_RealFileLeftAloneAndReported(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".config", "cfg")
	_, payload := setupNamespace(t, dest)
	mustSymlink(t, payload, dest)

	if err := os.Remove(dest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("real data, do not touch"), 0644); err != nil {
		t.Fatal(err)
	}

	problems, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(problems) != 1 {
		t.Fatalf("expected exactly one problem, got %+v", problems)
	}
	if problems[0].Dest != dest || problems[0].Namespace != "cfg-ns" || problems[0].Entry != "cfg" {
		t.Fatalf("unexpected problem: %+v", problems[0])
	}

	info, err := os.Lstat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected the real file at %s to be left alone, found a symlink", dest)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "real data, do not touch" {
		t.Fatalf("real file content changed: %q", data)
	}
}

// TestRun_EmptyDestinationNeverLinked covers: a manifest entry with an
// empty destination is never enabled and self-heal never tries to link it.
func TestRun_EmptyDestinationNeverLinked(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	reg := manifest.Registry{Repos: []manifest.Repo{{Name: "dotfiles"}}}
	if err := manifest.WriteRegistry(reg); err != nil {
		t.Fatal(err)
	}
	namespaceDir := filepath.Join(dataHome, "ireallylovemydots", "dotfiles", "cfg-ns")
	if err := os.MkdirAll(namespaceDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(namespaceDir, "cfg"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	m := manifest.Manifest{Entries: []manifest.Entry{{Name: "cfg", Dest: ""}}}
	if err := manifest.Write(namespaceDir, m); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(state.State{Entries: map[state.Key]state.Entry{
		{Repo: "dotfiles", Namespace: "cfg-ns"}: {Enabled: true},
	}}); err != nil {
		t.Fatal(err)
	}

	problems, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("expected no problems for an empty-destination entry, got %+v", problems)
	}
}

// TestRun_UninstalledNamespaceNotDrift covers: a registered but unenabled
// namespace, which has no payloads on disk, loses no manifest entries and
// reports no orphans — concept.md "An uninstalled namespace is not drift."
func TestRun_UninstalledNamespaceNotDrift(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	reg := manifest.Registry{Repos: []manifest.Repo{{Name: "dotfiles"}}}
	if err := manifest.WriteRegistry(reg); err != nil {
		t.Fatal(err)
	}
	// The namespace is registered (its repo exists) but never materialized
	// — no directory on disk at all — and state carries no entry for it,
	// exactly the "=" resting state.
	repoDir := filepath.Join(dataHome, "ireallylovemydots", "dotfiles")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}

	problems, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("expected no problems for an uninstalled namespace, got %+v", problems)
	}
}

// TestRun_MovedDestinationRemovesStaleLinkAndCreatesNew covers: changing a
// destination in the manifest and re-running removes the old link and
// creates the new one with no dangling link left behind.
func TestRun_MovedDestinationRemovesStaleLinkAndCreatesNew(t *testing.T) {
	home := t.TempDir()
	oldDest := filepath.Join(home, ".config", "old")
	namespaceDir, payload := setupNamespace(t, oldDest)
	mustSymlink(t, payload, oldDest)

	newDest := filepath.Join(home, ".config", "new")
	m := manifest.Manifest{Entries: []manifest.Entry{{Name: "cfg", Dest: newDest}}}
	if err := manifest.Write(namespaceDir, m); err != nil {
		t.Fatal(err)
	}

	problems, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("expected no problems, got %+v", problems)
	}

	if _, err := os.Lstat(oldDest); !os.IsNotExist(err) {
		t.Fatalf("expected the stale link at %s to be gone, got err=%v", oldDest, err)
	}
	if got := readLink(t, newDest); got != payload {
		t.Fatalf("expected %s to link to %s, got %s", newDest, payload, got)
	}

	s, err := state.Read()
	if err != nil {
		t.Fatal(err)
	}
	entry := s.Entries[state.Key{Repo: "dotfiles", Namespace: "cfg-ns"}]
	if len(entry.LinkedDests) != 1 || entry.LinkedDests[0] != newDest {
		t.Fatalf("expected state to record only the new destination, got %v", entry.LinkedDests)
	}
}

// TestRun_NeverWritesManifest asserts self-heal's central rule: it never
// writes to a .dots file, even while correcting drift.
func TestRun_NeverWritesManifest(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".config", "cfg")
	namespaceDir, payload := setupNamespace(t, dest)
	mustSymlink(t, payload, dest)
	if err := os.Remove(dest); err != nil {
		t.Fatal(err)
	}

	manifestPath := manifest.Path(namespaceDir)
	before, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	after, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	afterBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if before.ModTime() != after.ModTime() {
		t.Fatalf("manifest mtime changed: %v -> %v", before.ModTime(), after.ModTime())
	}
	if string(beforeBytes) != string(afterBytes) {
		t.Fatalf("manifest content changed")
	}
}

// TestRun_FiftyEntriesAddsNoMeasurableLatency is a generous-headroom timing
// check, not a benchmark harness: fifty already-correct symlinks should
// reconcile in well under a second.
func TestRun_FiftyEntriesAddsNoMeasurableLatency(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	home := t.TempDir()

	reg := manifest.Registry{Repos: []manifest.Repo{{Name: "dotfiles"}}}
	if err := manifest.WriteRegistry(reg); err != nil {
		t.Fatal(err)
	}
	namespaceDir := filepath.Join(dataHome, "ireallylovemydots", "dotfiles", "cfg-ns")
	if err := os.MkdirAll(namespaceDir, 0755); err != nil {
		t.Fatal(err)
	}

	const n = 50
	var entries []manifest.Entry
	var dests []string
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("f%d", i)
		payload := filepath.Join(namespaceDir, name)
		if err := os.WriteFile(payload, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(home, name)
		entries = append(entries, manifest.Entry{Name: name, Dest: dest})
		mustSymlink(t, payload, dest)
		dests = append(dests, dest)
	}
	if err := manifest.Write(namespaceDir, manifest.Manifest{Entries: entries}); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(state.State{Entries: map[state.Key]state.Entry{
		{Repo: "dotfiles", Namespace: "cfg-ns"}: {Enabled: true, LinkedDests: dests},
	}}); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	problems, err := Run()
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("expected no problems, got %+v", problems)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Run took %v for %d entries, expected well under a second", elapsed, n)
	}
}
