package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/state"
)

func TestEnable_OccupiedDestinationTrashedThenLinked(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	// Trash lives under $XDG_DATA_HOME, which must share a filesystem with
	// the temp-dir destination being trashed, or the rename it uses fails
	// with "invalid cross-device link".
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	home := t.TempDir()
	nsDir := t.TempDir()
	payload := filepath.Join(nsDir, "config")
	if err := os.MkdirAll(payload, 0755); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(home, ".config", "existing")
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("pre-existing content"), 0644); err != nil {
		t.Fatal(err)
	}

	entries := []manifest.Entry{{Name: "config", Dest: dest}}
	key := state.Key{Repo: "dotfiles", Namespace: "editors"}
	s := state.State{Entries: map[state.Key]state.Entry{}}

	problems, err := Preflight(key, nsDir, entries, s)
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if len(problems) != 1 || problems[0].Kind != Occupied {
		t.Fatalf("expected exactly one Occupied problem, got %+v", problems)
	}

	trashed, err := Enable(key, nsDir, nsDir, "editors", entries, s, problems)
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if len(trashed) != 1 || trashed[0].Dest != dest {
		t.Fatalf("expected Enable to report the trashed destination %s, got %+v", dest, trashed)
	}

	info, err := os.Lstat(dest)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected the occupied destination replaced by a symlink: %v", err)
	}
	target, err := os.Readlink(dest)
	if err != nil || target != payload {
		t.Fatalf("expected the symlink to point at the namespace payload, got %s (err=%v)", target, err)
	}
}

// TestEnable_AbsorbsDanglingSymlinkEmptyDirAndLiveSymlink covers the bug
// concept.md "Occupied destinations" already promised was impossible:
// pre-flight classifies an empty directory, a dangling symlink, and a live
// symlink pointing elsewhere as not occupied, but until this fix Enable
// still called os.Symlink straight at each one and failed EEXIST — a
// destination in exactly the state Preflight had just certified as safe.
func TestEnable_AbsorbsDanglingSymlinkEmptyDirAndLiveSymlink(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	home := t.TempDir()
	nsDir := t.TempDir()

	for _, name := range []string{"dangling", "emptydir", "live"} {
		payload := filepath.Join(nsDir, name)
		if err := os.MkdirAll(payload, 0755); err != nil {
			t.Fatal(err)
		}
	}

	danglingDest := filepath.Join(home, "dangling")
	if err := os.Symlink(filepath.Join(home, "gone"), danglingDest); err != nil {
		t.Fatal(err)
	}
	emptyDirDest := filepath.Join(home, "emptydir")
	if err := os.MkdirAll(emptyDirDest, 0755); err != nil {
		t.Fatal(err)
	}
	liveTarget := filepath.Join(home, "live-elsewhere")
	if err := os.WriteFile(liveTarget, []byte("z"), 0644); err != nil {
		t.Fatal(err)
	}
	liveDest := filepath.Join(home, "live")
	if err := os.Symlink(liveTarget, liveDest); err != nil {
		t.Fatal(err)
	}

	entries := []manifest.Entry{
		{Name: "dangling", Dest: danglingDest},
		{Name: "emptydir", Dest: emptyDirDest},
		{Name: "live", Dest: liveDest},
	}
	key := state.Key{Repo: "dotfiles", Namespace: "editors"}
	s := state.State{Entries: map[state.Key]state.Entry{}}

	problems, err := Preflight(key, nsDir, entries, s)
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("expected no pre-flight problems, got %+v", problems)
	}

	replaced, err := Enable(key, nsDir, nsDir, "editors", entries, s, problems)
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	// Absorbing a symlink or an empty directory holds nothing of the user's
	// and stays silent, per concept.md "Occupied destinations" — only real
	// files/directories routed through the trash path are reported.
	if len(replaced) != 0 {
		t.Fatalf("expected nothing reported, got %+v", replaced)
	}

	for _, e := range entries {
		info, err := os.Lstat(e.Dest)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("expected %s replaced by a symlink: %v", e.Dest, err)
		}
		target, err := os.Readlink(e.Dest)
		wantTarget := filepath.Join(nsDir, e.Name)
		if err != nil || target != wantTarget {
			t.Fatalf("expected %s to point at %s, got %s (err=%v)", e.Dest, wantTarget, target, err)
		}
	}

	// The live symlink's original target must be untouched — only the
	// pointer at liveDest was replaced, not the data it pointed to.
	data, err := os.ReadFile(liveTarget)
	if err != nil || string(data) != "z" {
		t.Fatalf("expected the live symlink's original target left alone, got data=%q err=%v", data, err)
	}
}
