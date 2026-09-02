package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeprecatedLuar/dotz/internal/link"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/profile"
	"github.com/DeprecatedLuar/dotz/internal/state"
)

// profiledNamespace builds an enabled namespace with four entries:
//
//	both      - overridden by dark and by light
//	darkonly  - overridden by dark only
//	neither   - profiled, overridden by no profile
//	plain     - not profiled at all
//
// and one directory entry, tree, overridden by dark, for the
// directory-links-whole check.
func profiledNamespace(t *testing.T) (nsDir string, entries []manifest.Entry, key state.Key, s state.State) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	home := t.TempDir()
	repoDir := t.TempDir()
	nsDir = filepath.Join(repoDir, "editors")
	if err := os.MkdirAll(nsDir, 0755); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"both", "darkonly", "neither", "plain"} {
		if err := os.WriteFile(filepath.Join(nsDir, name), []byte("root "+name), 0644); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, manifest.Entry{Name: name, Dest: filepath.Join(home, name)})
	}
	treeDir := filepath.Join(nsDir, "tree")
	if err := os.MkdirAll(treeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(treeDir, "inner"), []byte("root tree"), 0644); err != nil {
		t.Fatal(err)
	}
	entries = append(entries, manifest.Entry{Name: "tree", Dest: filepath.Join(home, "tree")})

	if err := manifest.Write(nsDir, manifest.Manifest{Entries: entries}); err != nil {
		t.Fatal(err)
	}
	if err := profile.Write(nsDir, profile.Manifest{Entries: []string{"both", "darkonly", "neither", "tree"}}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"dark", "light"} {
		if err := profile.Create(nsDir, name, ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"both", "darkonly", "tree"} {
		if err := profile.AddOverride(nsDir, "dark", name); err != nil {
			t.Fatal(err)
		}
	}
	if err := profile.AddOverride(nsDir, "light", "both"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile.ProfileDir(nsDir, "dark"), "both"), []byte("dark both"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile.ProfileDir(nsDir, "light"), "both"), []byte("light both"), 0644); err != nil {
		t.Fatal(err)
	}

	key = state.Key{Repo: "dotfiles", Namespace: "editors"}
	s = state.State{Entries: map[state.Key]state.Entry{}}
	if _, err := Enable(key, repoDir, nsDir, "editors", entries, s, nil); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	return nsDir, entries, key, s
}

func destOf(entries []manifest.Entry, name string) string {
	for _, e := range entries {
		if e.Name == name {
			return e.Dest
		}
	}
	return ""
}

func linkTarget(t *testing.T, dest string) string {
	t.Helper()
	target, err := link.Read(dest)
	if err != nil {
		t.Fatalf("read link %s: %v", dest, err)
	}
	return target
}

func linkMtime(t *testing.T, path string) time.Time {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	return info.ModTime()
}

func TestSwitchProfile_ChangesDestinationContentAndLeavesRootUntouched(t *testing.T) {
	nsDir, entries, key, s := profiledNamespace(t)
	root := filepath.Join(nsDir, "both")
	rootBefore, err := os.ReadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	rootMtimeBefore := linkMtime(t, root)

	if _, err := SwitchProfile(key, nsDir, entries, s, "dark"); err != nil {
		t.Fatalf("SwitchProfile: %v", err)
	}
	got, err := os.ReadFile(destOf(entries, "both"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "dark both" {
		t.Fatalf("destination content = %q, want dark's version", got)
	}

	rootAfter, err := os.ReadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	if string(rootAfter) != string(rootBefore) {
		t.Fatalf("root copy changed: %q -> %q", rootBefore, rootAfter)
	}
	if !linkMtime(t, root).Equal(rootMtimeBefore) {
		t.Fatal("root copy's mtime changed")
	}
}

func TestSwitchProfile_RelinksOnlyEntriesWhoseTargetChanges(t *testing.T) {
	nsDir, entries, key, s := profiledNamespace(t)
	if _, err := SwitchProfile(key, nsDir, entries, s, "dark"); err != nil {
		t.Fatalf("SwitchProfile to dark: %v", err)
	}

	untouched := map[string]time.Time{}
	for _, name := range []string{"neither", "plain"} {
		untouched[name] = linkMtime(t, destOf(entries, name))
	}
	darkOnlyTarget := linkTarget(t, destOf(entries, "darkonly"))

	// A symlink's own mtime has one-second granularity on some filesystems,
	// so a relink that happens within the same second would be
	// indistinguishable from no relink at all.
	time.Sleep(1100 * time.Millisecond)

	relinked, err := SwitchProfile(key, nsDir, entries, s, "light")
	if err != nil {
		t.Fatalf("SwitchProfile to light: %v", err)
	}

	// both: overridden by dark and by light, so its target changes.
	// darkonly: overridden by dark only, so it falls back to the root copy.
	if len(relinked) != 3 {
		t.Fatalf("relinked = %v, want both, darkonly and tree", relinked)
	}
	for _, name := range []string{"neither", "plain"} {
		if !linkMtime(t, destOf(entries, name)).Equal(untouched[name]) {
			t.Fatalf("%s was relinked but neither profile overrides it", name)
		}
	}
	if linkTarget(t, destOf(entries, "darkonly")) == darkOnlyTarget {
		t.Fatal("darkonly still points at dark's override")
	}
	if linkTarget(t, destOf(entries, "darkonly")) != filepath.Join(nsDir, "darkonly") {
		t.Fatal("darkonly did not fall back to the root copy")
	}
}

func TestSwitchProfile_ProfiledDirectoryLinksWhole(t *testing.T) {
	nsDir, entries, key, s := profiledNamespace(t)
	if _, err := SwitchProfile(key, nsDir, entries, s, "dark"); err != nil {
		t.Fatalf("SwitchProfile: %v", err)
	}

	dest := destOf(entries, "tree")
	info, err := os.Lstat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("destination is not a symlink")
	}
	if linkTarget(t, dest) != filepath.Join(profile.ProfileDir(nsDir, "dark"), "tree") {
		t.Fatalf("target = %q, want the profile's copy of the whole directory", linkTarget(t, dest))
	}
}

func TestSwitchProfile_EmptyProfileResolvesEveryEntryToRoot(t *testing.T) {
	nsDir, entries, key, s := profiledNamespace(t)
	if err := profile.Create(nsDir, "bare", ""); err != nil {
		t.Fatal(err)
	}

	relinked, err := SwitchProfile(key, nsDir, entries, s, "bare")
	if err != nil {
		t.Fatalf("SwitchProfile: %v", err)
	}
	if len(relinked) != 0 {
		t.Fatalf("relinked = %v, want nothing: every entry already resolves to the root", relinked)
	}
	for _, e := range entries {
		if linkTarget(t, e.Dest) != filepath.Join(nsDir, e.Name) {
			t.Fatalf("%s does not point at the root copy", e.Name)
		}
	}
}

func TestSwitchProfile_BackToMainRecordsEmptyActiveProfile(t *testing.T) {
	nsDir, entries, key, s := profiledNamespace(t)
	if _, err := SwitchProfile(key, nsDir, entries, s, "dark"); err != nil {
		t.Fatal(err)
	}
	if _, err := SwitchProfile(key, nsDir, entries, s, profile.Main); err != nil {
		t.Fatalf("SwitchProfile to main: %v", err)
	}

	persisted, err := state.Read()
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Entries[key].ActiveProfile != "" {
		t.Fatalf("active profile = %q, want main encoded as an empty string", persisted.Entries[key].ActiveProfile)
	}
	if linkTarget(t, destOf(entries, "both")) != filepath.Join(nsDir, "both") {
		t.Fatal("both did not return to the root copy")
	}
}

func TestSwitchProfile_DisabledNamespaceRecordsOnlyState(t *testing.T) {
	nsDir, entries, key, s := profiledNamespace(t)
	if err := Disable(key, s); err != nil {
		t.Fatal(err)
	}

	if _, err := SwitchProfile(key, nsDir, entries, s, "dark"); err != nil {
		t.Fatalf("SwitchProfile: %v", err)
	}
	persisted, err := state.Read()
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Entries[key].ActiveProfile != "dark" {
		t.Fatalf("active profile = %q", persisted.Entries[key].ActiveProfile)
	}
	if _, err := os.Lstat(destOf(entries, "both")); !os.IsNotExist(err) {
		t.Fatalf("a disabled namespace should have no links, got err=%v", err)
	}
}

func TestEnable_LinksTheActiveProfilesVersion(t *testing.T) {
	nsDir, entries, key, s := profiledNamespace(t)
	if _, err := SwitchProfile(key, nsDir, entries, s, "dark"); err != nil {
		t.Fatal(err)
	}
	if err := Disable(key, s); err != nil {
		t.Fatal(err)
	}
	if _, err := Enable(key, filepath.Dir(nsDir), nsDir, "editors", entries, s, nil); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	if linkTarget(t, destOf(entries, "both")) != filepath.Join(profile.ProfileDir(nsDir, "dark"), "both") {
		t.Fatal("enable did not link the active profile's version")
	}
}
