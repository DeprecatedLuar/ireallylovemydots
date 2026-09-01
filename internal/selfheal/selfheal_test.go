package selfheal

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/state"
)

// initGitRepo turns dir into a git repository with everything currently in
// it committed, so a test can delete a file from the working tree afterward
// and still have git.ShowFile recover it from HEAD — exactly the situation
// self-heal's manifest recovery exists for.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", ".")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	run("add", "-A")
	run("commit", "-q", "-m", "initial")
}

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

	findings, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(findings.Problems) != 0 {
		t.Fatalf("expected no problems, got %+v", findings.Problems)
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

	findings, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(findings.Problems) != 0 {
		t.Fatalf("expected no problems, got %+v", findings.Problems)
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

	findings, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(findings.Problems) != 0 {
		t.Fatalf("expected no bare problems, the namespace should be auto-disabled instead, got %+v", findings.Problems)
	}
	if len(findings.Disabled) != 1 {
		t.Fatalf("expected exactly one disabled namespace, got %+v", findings.Disabled)
	}
	d := findings.Disabled[0]
	if d.Repo != "dotfiles" || d.Namespace != "cfg-ns" {
		t.Fatalf("unexpected disabled namespace: %+v", d)
	}
	if len(d.Reasons) != 1 || d.Reasons[0].Dest != dest || d.Reasons[0].Entry != "cfg" {
		t.Fatalf("unexpected disable reasons: %+v", d.Reasons)
	}

	s, err := state.Read()
	if err != nil {
		t.Fatal(err)
	}
	if s.Entries[state.Key{Repo: "dotfiles", Namespace: "cfg-ns"}].Enabled {
		t.Fatal("expected the namespace to be flipped to disabled")
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

// TestRun_IgnoredNamespace_SkippedEntirely covers concept.md "Namespace": a
// namespace whose manifest carries ignore = true is skipped before
// inspection or reconciliation, even in the shape that would otherwise force
// an auto-disable (TestRun_RealFileLeftAloneAndReported) — no Problem, no
// Disabled, no NeedsRepair, and the occupying real file is left untouched.
func TestRun_IgnoredNamespace_SkippedEntirely(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".config", "cfg")
	namespaceDir, payload := setupNamespace(t, dest)
	mustSymlink(t, payload, dest)

	if err := os.Remove(dest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("real data, do not touch"), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := manifest.Read(namespaceDir)
	if err != nil {
		t.Fatal(err)
	}
	m.Ignore = true
	if err := manifest.Write(namespaceDir, m); err != nil {
		t.Fatal(err)
	}

	findings, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(findings.Disabled) != 0 {
		t.Fatalf("expected an ignored namespace never to be auto-disabled, got %+v", findings.Disabled)
	}
	if len(findings.NeedsRepair) != 0 {
		t.Fatalf("expected an ignored namespace to need no repair, got %+v", findings.NeedsRepair)
	}

	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "real data, do not touch" {
		t.Fatalf("real file content changed: %q", data)
	}
}

// TestRun_IgnoredButStateEnabled_ReportsContradiction covers the one case
// self-heal cannot silently skip: hand-edited state records an ignored
// namespace as still enabled. Neither side is acted on — no reconciliation,
// no auto-disable — since ignore's own command refuses that combination
// (concept.md "Namespace"); self-heal instead reports the contradiction so a
// human resolves it.
func TestRun_IgnoredButStateEnabled_ReportsContradiction(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".config", "cfg")
	namespaceDir, payload := setupNamespace(t, dest)
	mustSymlink(t, payload, dest)

	m, err := manifest.Read(namespaceDir)
	if err != nil {
		t.Fatal(err)
	}
	m.Ignore = true
	if err := manifest.Write(namespaceDir, m); err != nil {
		t.Fatal(err)
	}

	findings, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(findings.Disabled) != 0 {
		t.Fatalf("expected no auto-disable for the contradiction, got %+v", findings.Disabled)
	}
	if len(findings.Problems) != 1 || findings.Problems[0].Namespace != "cfg-ns" {
		t.Fatalf("expected exactly one problem naming the contradiction, got %+v", findings.Problems)
	}
}

// TestRun_PartiallyLinkedNamespaceIsFullyUnlinkedAndDisabled covers: a
// namespace with one entry blocked and one entry that linked cleanly is not
// left half-true — concept.md "Self-healing": "enabled means every entry's
// symlink is correct and healthy — nothing less." The working symlink is
// torn down along with the blocked one, and the namespace is disabled
// through the same path an explicit `dots disable` uses.
func TestRun_PartiallyLinkedNamespaceIsFullyUnlinkedAndDisabled(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	reg := manifest.Registry{Repos: []manifest.Repo{{Name: "dotfiles"}}}
	if err := manifest.WriteRegistry(reg); err != nil {
		t.Fatal(err)
	}

	namespaceDir := filepath.Join(dataHome, "ireallylovemydots", "dotfiles", "multi-ns")
	if err := os.MkdirAll(namespaceDir, 0755); err != nil {
		t.Fatal(err)
	}
	goodPayload := filepath.Join(namespaceDir, "good")
	if err := os.WriteFile(goodPayload, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	blockedPayload := filepath.Join(namespaceDir, "blocked")
	if err := os.WriteFile(blockedPayload, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	goodDest := filepath.Join(home, ".config", "good")
	blockedDest := filepath.Join(home, ".config", "blocked")

	m := manifest.Manifest{Entries: []manifest.Entry{
		{Name: "good", Dest: goodDest},
		{Name: "blocked", Dest: blockedDest},
	}}
	if err := manifest.Write(namespaceDir, m); err != nil {
		t.Fatal(err)
	}

	mustSymlink(t, goodPayload, goodDest)
	if err := os.MkdirAll(filepath.Dir(blockedDest), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blockedDest, []byte("real data"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := state.Write(state.State{Entries: map[state.Key]state.Entry{
		{Repo: "dotfiles", Namespace: "multi-ns"}: {Enabled: true, LinkedDests: []string{goodDest}},
	}}); err != nil {
		t.Fatal(err)
	}

	findings, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(findings.Disabled) != 1 || findings.Disabled[0].Namespace != "multi-ns" {
		t.Fatalf("expected multi-ns to be disabled, got %+v", findings.Disabled)
	}

	if _, err := os.Lstat(goodDest); !os.IsNotExist(err) {
		t.Fatalf("expected the previously-good symlink at %s to be removed too, lstat: %v", goodDest, err)
	}

	s, err := state.Read()
	if err != nil {
		t.Fatal(err)
	}
	if s.Entries[state.Key{Repo: "dotfiles", Namespace: "multi-ns"}].Enabled {
		t.Fatal("expected multi-ns to be disabled")
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

	findings, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(findings.Problems) != 0 {
		t.Fatalf("expected no problems for an empty-destination entry, got %+v", findings.Problems)
	}
	if len(findings.Disabled) != 0 {
		t.Fatalf("an empty-destination entry is a manifest problem, not a link failure, it must not auto-disable: %+v", findings.Disabled)
	}

	s, err := state.Read()
	if err != nil {
		t.Fatal(err)
	}
	if !s.Entries[state.Key{Repo: "dotfiles", Namespace: "cfg-ns"}].Enabled {
		t.Fatal("expected the namespace to stay enabled")
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

	findings, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(findings.Problems) != 0 {
		t.Fatalf("expected no problems for an uninstalled namespace, got %+v", findings.Problems)
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

	findings, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(findings.Problems) != 0 {
		t.Fatalf("expected no problems, got %+v", findings.Problems)
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

// TestRun_NewManifestEntryGetsLinked covers a namespace manifest gaining an
// entry after enable — e.g. pulled in by `sync` from another machine — with
// no prior record of it in state.LinkedDests. Self-heal must link it exactly
// like any other missing destination, and record it in state afterward, per
// concept.md "Self-healing": a namespace marked enabled means every current
// manifest entry is linked, not just the ones enable happened to see first.
func TestRun_NewManifestEntryGetsLinked(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".config", "cfg")
	namespaceDir, payload := setupNamespace(t, dest)
	mustSymlink(t, payload, dest)

	// A second entry lands in the manifest and its payload materializes on
	// disk — what `sync` does after pulling an updated .dots — but nothing
	// has linked it yet and state has never heard of it.
	newDest := filepath.Join(home, ".config", "new")
	newPayload := filepath.Join(namespaceDir, "new")
	if err := os.WriteFile(newPayload, []byte("z"), 0644); err != nil {
		t.Fatal(err)
	}
	m := manifest.Manifest{Entries: []manifest.Entry{
		{Name: "cfg", Dest: dest},
		{Name: "new", Dest: newDest},
	}}
	if err := manifest.Write(namespaceDir, m); err != nil {
		t.Fatal(err)
	}

	findings, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(findings.Problems) != 0 {
		t.Fatalf("expected no problems, got %+v", findings.Problems)
	}
	if got := readLink(t, newDest); got != newPayload {
		t.Fatalf("expected %s linked to %s, got %s", newDest, newPayload, got)
	}
	if got := readLink(t, dest); got != payload {
		t.Fatalf("expected existing link %s untouched, got %s", dest, got)
	}

	s, err := state.Read()
	if err != nil {
		t.Fatal(err)
	}
	entry := s.Entries[state.Key{Repo: "dotfiles", Namespace: "cfg-ns"}]
	if len(entry.LinkedDests) != 2 {
		t.Fatalf("expected state to record both destinations, got %v", entry.LinkedDests)
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
	findings, err := Run()
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(findings.Problems) != 0 {
		t.Fatalf("expected no problems, got %+v", findings.Problems)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Run took %v for %d entries, expected well under a second", elapsed, n)
	}
}

// dataDirGuardKeeper creates one throwaway directory inside the data
// directory so Run's DataDirEmpty guard does not trip in tests that are
// exercising the stranded-state or unregistered-clone paths specifically —
// those paths only make sense once the data directory is known to hold at
// least one real repository clone.
func dataDirGuardKeeper(t *testing.T, dataDir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dataDir, "keeper"), 0755); err != nil {
		t.Fatal(err)
	}
	reg := manifest.Registry{Repos: []manifest.Repo{{Name: "keeper"}}}
	if err := manifest.WriteRegistry(reg); err != nil {
		t.Fatal(err)
	}
}

// TestRun_StrandedState_OwnedDanglingLinkRemovedAndDropped covers: a state
// entry whose repository directory no longer exists in the data directory
// gets its still-owned symlink removed and its entry dropped — concept.md
// "The data directory can drift from the registry too".
func TestRun_StrandedState_OwnedDanglingLinkRemovedAndDropped(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dataDir := filepath.Join(dataHome, "ireallylovemydots")
	dataDirGuardKeeper(t, dataDir)

	home := t.TempDir()
	dest := filepath.Join(home, ".config", "gone-cfg")
	// "gone"'s directory is never created — the repository clone is simply
	// absent, as if it had been deleted outside dots.
	strandedPayload := filepath.Join(dataDir, "gone", "ns", "cfg")
	mustSymlink(t, strandedPayload, dest)

	key := state.Key{Repo: "gone", Namespace: "ns"}
	if err := state.Write(state.State{Entries: map[state.Key]state.Entry{
		key: {Enabled: true, LinkedDests: []string{dest}},
	}}); err != nil {
		t.Fatal(err)
	}

	findings, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(findings.Dropped) != 1 || findings.Dropped[0] != key {
		t.Fatalf("expected %v dropped, got %v", key, findings.Dropped)
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatalf("expected the owned dangling link at %s removed, got err=%v", dest, err)
	}

	s, err := state.Read()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Entries[key]; ok {
		t.Fatalf("expected state entry for %v to be dropped, still present", key)
	}
}

// TestRun_StrandedState_RepointedAndRealDestinationsLeftAlone covers: a
// stranded state entry's recorded destination is left untouched, only its
// entry dropped, when the destination is no longer dots' to touch — it has
// become a real file, or a symlink now pointing somewhere live.
func TestRun_StrandedState_RepointedAndRealDestinationsLeftAlone(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dataDir := filepath.Join(dataHome, "ireallylovemydots")
	dataDirGuardKeeper(t, dataDir)

	home := t.TempDir()
	realDest := filepath.Join(home, ".config", "real-now")
	if err := os.MkdirAll(filepath.Dir(realDest), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(realDest, []byte("real data, do not touch"), 0644); err != nil {
		t.Fatal(err)
	}

	elsewhere := filepath.Join(home, "elsewhere")
	if err := os.WriteFile(elsewhere, []byte("y"), 0644); err != nil {
		t.Fatal(err)
	}
	repointedDest := filepath.Join(home, ".config", "repointed")
	mustSymlink(t, elsewhere, repointedDest)

	key := state.Key{Repo: "gone", Namespace: "ns"}
	if err := state.Write(state.State{Entries: map[state.Key]state.Entry{
		key: {Enabled: true, LinkedDests: []string{realDest, repointedDest}},
	}}); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(realDest)
	if err != nil || string(data) != "real data, do not touch" {
		t.Fatalf("expected the real file untouched, got data=%q err=%v", data, err)
	}
	if got := readLink(t, repointedDest); got != elsewhere {
		t.Fatalf("expected the repointed symlink untouched, pointing at %s, got %s", elsewhere, got)
	}

	s, err := state.Read()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Entries[key]; ok {
		t.Fatalf("expected state entry for %v to be dropped even though its destinations were left alone", key)
	}
}

// TestRun_EmptyDataDir_ReconcilesNothing covers the guard: a data directory
// holding no repository clones at all reads identically to every repository
// having vanished at once, so Run must warn (DataDirEmpty) rather than drop
// every enabled state entry in one pass — concept.md "The data directory
// can drift from the registry too".
func TestRun_EmptyDataDir_ReconcilesNothing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	key := state.Key{Repo: "anything", Namespace: "ns"}
	before := state.State{Entries: map[state.Key]state.Entry{
		key: {Enabled: true, LinkedDests: []string{"/nonexistent/dest"}},
	}}
	if err := state.Write(before); err != nil {
		t.Fatal(err)
	}

	findings, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !findings.DataDirEmpty {
		t.Fatalf("expected DataDirEmpty, got %+v", findings)
	}
	if len(findings.Dropped) != 0 || len(findings.Unregistered) != 0 || len(findings.Problems) != 0 {
		t.Fatalf("expected an empty data dir to touch nothing, got %+v", findings)
	}

	s, err := state.Read()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Entries[key]; !ok {
		t.Fatalf("expected state entry for %v untouched, was dropped", key)
	}
}

// TestRun_UnregisteredClone_ReportedNeverRegistered covers: a directory in
// the data directory with no matching registry entry is reported by name,
// never auto-registered — concept.md "The data directory can drift from the
// registry too": the fix is `repo adopt`, a command, not self-heal itself.
func TestRun_UnregisteredClone_ReportedNeverRegistered(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dataDir := filepath.Join(dataHome, "ireallylovemydots")

	if err := os.MkdirAll(filepath.Join(dataDir, "orphan"), 0755); err != nil {
		t.Fatal(err)
	}
	// The registry names a different, unrelated repository — "orphan" is
	// never in it.
	reg := manifest.Registry{Repos: []manifest.Repo{{Name: "known"}}}
	if err := manifest.WriteRegistry(reg); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "known"), 0755); err != nil {
		t.Fatal(err)
	}

	findings, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(findings.Unregistered) != 1 || findings.Unregistered[0] != "orphan" {
		t.Fatalf("expected [orphan] unregistered, got %v", findings.Unregistered)
	}

	reg2, err := manifest.ReadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range reg2.Repos {
		if r.Name == "orphan" {
			t.Fatalf("expected orphan never auto-registered, found it in the registry")
		}
	}
}

// TestRun_MissingManifest_NeverUnlinks is the regression test for the
// reported bug: deleting .dots used to read as "every entry was cleared,"
// so self-heal treated the namespace's recorded link as stale and removed
// it — destroying a working symlink and the state needed to rebuild it, on
// a namespace nothing was actually wrong with. This must not happen
// regardless of which rung of the recovery ladder catches the namespace; no
// git repository exists here, forcing the worst case.
func TestRun_MissingManifest_NeverUnlinks(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".config", "cfg")
	namespaceDir, payload := setupNamespace(t, dest)
	mustSymlink(t, payload, dest)

	if err := os.Remove(manifest.Path(namespaceDir)); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := readLink(t, dest); got != payload {
		t.Fatalf("expected the existing link at %s to survive, got %s", dest, got)
	}
	s, err := state.Read()
	if err != nil {
		t.Fatal(err)
	}
	entry := s.Entries[state.Key{Repo: "dotfiles", Namespace: "cfg-ns"}]
	if len(entry.LinkedDests) != 1 || entry.LinkedDests[0] != dest {
		t.Fatalf("expected state to still record %s, got %v", dest, entry.LinkedDests)
	}
}

// TestRun_MissingManifest_RecoveredFromGit covers the highest rung: a
// namespace whose .dots was committed recovers byte-for-byte from HEAD, with
// no invented entries — git is a higher-fidelity witness than anything left
// on disk.
func TestRun_MissingManifest_RecoveredFromGit(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".config", "cfg")
	namespaceDir, payload := setupNamespace(t, dest)
	mustSymlink(t, payload, dest)
	initGitRepo(t, filepath.Dir(namespaceDir))

	if err := os.Remove(manifest.Path(namespaceDir)); err != nil {
		t.Fatal(err)
	}

	findings, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(findings.Recovered) != 1 || findings.Recovered[0].Source != RecoveredFromGit {
		t.Fatalf("expected one git-sourced recovery, got %+v", findings.Recovered)
	}
	for _, r := range findings.NeedsRepair {
		if r.Namespace == "cfg-ns" {
			t.Fatalf("a fully git-recovered manifest should need no repair, got %+v", r.Report)
		}
	}

	m, err := manifest.Read(namespaceDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entries) != 1 || m.Entries[0].Name != "cfg" || m.Entries[0].Dest != dest {
		t.Fatalf("expected the manifest restored exactly as committed, got %+v", m.Entries)
	}
}

// TestRun_MissingManifest_RecoveredFromLinks covers the middle rung: no git
// history to fall back on, but the destination is still a live symlink into
// the namespace folder, which is proof enough to reconstruct the entry.
func TestRun_MissingManifest_RecoveredFromLinks(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".config", "cfg")
	namespaceDir, payload := setupNamespace(t, dest)
	mustSymlink(t, payload, dest)

	if err := os.Remove(manifest.Path(namespaceDir)); err != nil {
		t.Fatal(err)
	}

	findings, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(findings.Recovered) != 1 || findings.Recovered[0].Source != RecoveredFromLinks {
		t.Fatalf("expected one link-sourced recovery, got %+v", findings.Recovered)
	}

	m, err := manifest.Read(namespaceDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entries) != 1 || m.Entries[0].Name != "cfg" || m.Entries[0].Dest != dest {
		t.Fatalf("expected the manifest reconstructed from its live link, got %+v", m.Entries)
	}
}

// TestRun_MissingManifest_ScaffoldOnly covers the last rung: no git history
// and no live link to recover from (the real-world case this bug produced —
// a prior run had already wiped state.LinkedDests before this fix existed).
// The payload is named in the manifest with a blank destination rather than
// dropped, it is reported as needing repair, and nothing gets linked on its
// behalf.
func TestRun_MissingManifest_ScaffoldOnly(t *testing.T) {
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
	// No .dots at all, and state records the namespace enabled with no
	// linked destinations to reconstruct from — nothing to prove ownership
	// with.
	if err := state.Write(state.State{Entries: map[state.Key]state.Entry{
		{Repo: "dotfiles", Namespace: "cfg-ns"}: {Enabled: true},
	}}); err != nil {
		t.Fatal(err)
	}

	findings, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(findings.Recovered) != 1 || findings.Recovered[0].Source != RecoveredScaffoldOnly {
		t.Fatalf("expected one scaffold-only recovery, got %+v", findings.Recovered)
	}
	if len(findings.NeedsRepair) != 1 || findings.NeedsRepair[0].Namespace != "cfg-ns" {
		t.Fatalf("expected cfg-ns reported as needing repair, got %+v", findings.NeedsRepair)
	}

	m, err := manifest.Read(namespaceDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entries) != 1 || m.Entries[0].Name != "cfg" || m.Entries[0].Dest != "" {
		t.Fatalf("expected cfg scaffolded with a blank destination, got %+v", m.Entries)
	}
}
