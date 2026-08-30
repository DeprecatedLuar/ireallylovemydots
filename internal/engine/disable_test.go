package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/state"
)

func TestDisable_RemovesLinksKeepsFilesFlipsState(t *testing.T) {
	home := t.TempDir()
	nsDir := t.TempDir()
	payload := filepath.Join(nsDir, "nvim")
	if err := os.MkdirAll(payload, 0755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(home, ".config", "nvim")
	entries := []manifest.Entry{{Name: "nvim", Dest: dest}}

	key := state.Key{Repo: "dotfiles", Namespace: "editors"}
	s := state.State{Entries: map[state.Key]state.Entry{}}
	if err := Enable(key, nsDir, nsDir, "editors", entries, s, nil); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	if err := Disable(key, s); err != nil {
		t.Fatalf("Disable: %v", err)
	}

	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatalf("expected the symlink removed, got err=%v", err)
	}
	if _, err := os.Stat(payload); err != nil {
		t.Fatalf("expected the payload to stay on disk: %v", err)
	}
	entry := s.Entries[key]
	if entry.Enabled {
		t.Fatal("expected the state entry to be flipped to disabled")
	}
	if len(entry.LinkedDests) != 0 {
		t.Fatalf("expected no recorded links after disable, got %+v", entry.LinkedDests)
	}
}

func TestDisable_UnenabledNamespaceIsNoOp(t *testing.T) {
	key := state.Key{Repo: "dotfiles", Namespace: "editors"}
	s := state.State{Entries: map[state.Key]state.Entry{}}
	if err := Disable(key, s); err != nil {
		t.Fatalf("expected disabling a namespace with no state entry to be a no-op, got %v", err)
	}
}

func TestEnable_CollisionDisablesWholeConflictingNamespace(t *testing.T) {
	home := t.TempDir()
	nsDir := t.TempDir()

	oldPayload := filepath.Join(nsDir, "old")
	if err := os.MkdirAll(oldPayload, 0755); err != nil {
		t.Fatal(err)
	}
	sharedDest := filepath.Join(home, ".config", "nvim")
	otherDest := filepath.Join(home, ".config", "other-only")
	if err := os.MkdirAll(filepath.Dir(sharedDest), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(oldPayload, sharedDest); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(oldPayload, otherDest); err != nil {
		t.Fatal(err)
	}

	oldKey := state.Key{Repo: "dotfiles", Namespace: "old"}
	s := state.State{Entries: map[state.Key]state.Entry{
		oldKey: {Enabled: true, LinkedDests: []string{sharedDest, otherDest}},
	}}

	newPayload := filepath.Join(nsDir, "new")
	if err := os.MkdirAll(newPayload, 0755); err != nil {
		t.Fatal(err)
	}
	entries := []manifest.Entry{{Name: "new", Dest: sharedDest}}
	newKey := state.Key{Repo: "dotfiles", Namespace: "new"}

	problems, err := Preflight(newKey, nsDir, entries, s)
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if len(problems) != 1 || problems[0].Kind != Collision {
		t.Fatalf("expected exactly one Collision problem, got %+v", problems)
	}

	if err := Enable(newKey, nsDir, nsDir, "new", entries, s, problems); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	if entry := s.Entries[oldKey]; entry.Enabled {
		t.Fatal("expected the entire conflicting namespace to be disabled, not just the overlapping destination")
	}
	if _, err := os.Lstat(otherDest); !os.IsNotExist(err) {
		t.Fatalf("expected the conflicting namespace's other, non-overlapping link removed too, got err=%v", err)
	}

	target, err := os.Readlink(sharedDest)
	if err != nil {
		t.Fatal(err)
	}
	if target != newPayload {
		t.Fatalf("expected the destination to now point at the new namespace's payload, got %s", target)
	}
}
