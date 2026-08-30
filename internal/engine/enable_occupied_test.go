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

	if err := Enable(key, nsDir, nsDir, "editors", entries, s, problems); err != nil {
		t.Fatalf("Enable: %v", err)
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
