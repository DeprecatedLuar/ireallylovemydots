package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/state"
)

func TestRestorePreflight_AbsentEmptyDirDanglingSymlink_NotOccupied(t *testing.T) {
	home := t.TempDir()
	nsDir := t.TempDir()

	absent := filepath.Join(home, "absent")
	emptyDir := filepath.Join(home, "empty")
	if err := os.MkdirAll(emptyDir, 0755); err != nil {
		t.Fatal(err)
	}
	danglingTarget := filepath.Join(home, "gone")
	dangling := filepath.Join(home, "dangling")
	if err := os.Symlink(danglingTarget, dangling); err != nil {
		t.Fatal(err)
	}

	entries := []manifest.Entry{
		{Name: "absent", Dest: absent},
		{Name: "empty", Dest: emptyDir},
		{Name: "dangling", Dest: dangling},
	}
	problems, err := RestorePreflight(nsDir, "editors", entries)
	if err != nil {
		t.Fatalf("RestorePreflight: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("expected no problems for absent/empty-dir/dangling-symlink destinations, got %+v", problems)
	}
}

func TestRestorePreflight_CorrectSymlinkNotOccupied(t *testing.T) {
	home := t.TempDir()
	nsDir := t.TempDir()
	payload := filepath.Join(nsDir, "nvim")
	if err := os.MkdirAll(payload, 0755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(payload, dest); err != nil {
		t.Fatal(err)
	}

	entries := []manifest.Entry{{Name: "nvim", Dest: dest}}
	problems, err := RestorePreflight(nsDir, "editors", entries)
	if err != nil {
		t.Fatalf("RestorePreflight: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("expected the entry's own correct symlink not to be flagged occupied, got %+v", problems)
	}
}

func TestRestorePreflight_OccupiedRealFileNonEmptyDirAndLiveSymlink(t *testing.T) {
	home := t.TempDir()
	nsDir := t.TempDir()

	realFile := filepath.Join(home, "config")
	if err := os.WriteFile(realFile, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	nonEmptyDir := filepath.Join(home, "ssh")
	if err := os.MkdirAll(nonEmptyDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nonEmptyDir, "id_rsa"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	liveTarget := filepath.Join(home, "elsewhere")
	if err := os.WriteFile(liveTarget, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	liveSymlink := filepath.Join(home, "other-link")
	if err := os.Symlink(liveTarget, liveSymlink); err != nil {
		t.Fatal(err)
	}

	entries := []manifest.Entry{
		{Name: "config", Dest: realFile},
		{Name: "ssh", Dest: nonEmptyDir},
		{Name: "other-link", Dest: liveSymlink},
	}
	problems, err := RestorePreflight(nsDir, "editors", entries)
	if err != nil {
		t.Fatalf("RestorePreflight: %v", err)
	}
	if len(problems) != 3 {
		t.Fatalf("expected 3 occupied problems, got %+v", problems)
	}
	for _, p := range problems {
		if !strings.Contains(p.Message, "[t]") || !strings.Contains(p.Message, "[p]") || !strings.Contains(p.Message, "[c]") {
			t.Fatalf("expected the message to name all three choices, got: %s", p.Message)
		}
	}
}

func TestRestore_DisabledNamespace_WritesRealFileToEmptyDestination(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	home := t.TempDir()
	nsDir := t.TempDir()
	payload := filepath.Join(nsDir, "nvim")
	if err := os.MkdirAll(payload, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "init.lua"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(home, ".config", "nvim")
	entries := []manifest.Entry{{Name: "nvim", Dest: dest}}
	if err := manifest.Write(nsDir, manifest.Manifest{Entries: entries}); err != nil {
		t.Fatal(err)
	}

	key := state.Key{Repo: "dotfiles", Namespace: "editors"}
	s := state.State{Entries: map[state.Key]state.Entry{}}

	if err := Restore(key, nsDir, entries, nil, s, false); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	info, err := os.Lstat(dest)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected a real directory at %s, got err=%v mode=%v", dest, err, info)
	}
	if _, err := os.Stat(filepath.Join(dest, "init.lua")); err != nil {
		t.Fatalf("expected the payload's contents at the destination: %v", err)
	}
	if _, err := os.Stat(payload); !os.IsNotExist(err) {
		t.Fatalf("expected the payload gone from the namespace, got err=%v", err)
	}

	m, err := manifest.Read(nsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entries) != 0 {
		t.Fatalf("expected no manifest entries left, got %+v", m.Entries)
	}
}

func TestRestore_EnabledNamespace_RemovesSymlinkWritesRealFileAndNarrowsState(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	home := t.TempDir()
	nsDir := t.TempDir()
	payload := filepath.Join(nsDir, "nvim")
	if err := os.MkdirAll(payload, 0755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(home, ".config", "nvim")

	entries := []manifest.Entry{{Name: "nvim", Dest: dest}}
	if err := manifest.Write(nsDir, manifest.Manifest{Entries: entries}); err != nil {
		t.Fatal(err)
	}

	key := state.Key{Repo: "dotfiles", Namespace: "editors"}
	s := state.State{Entries: map[state.Key]state.Entry{}}
	if err := Enable(key, nsDir, nsDir, "editors", entries, s, nil); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	if err := Restore(key, nsDir, entries, nil, s, false); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	info, err := os.Lstat(dest)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected the symlink replaced by a real directory at %s, got err=%v", dest, err)
	}
	if entry := s.Entries[key]; len(entry.LinkedDests) != 0 {
		t.Fatalf("expected the restored destination dropped from state, got %+v", entry.LinkedDests)
	}
}

func TestRestore_OccupiedDestination_TrashesOccupantAndRestoresOurs(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	home := t.TempDir()
	nsDir := t.TempDir()
	payload := filepath.Join(nsDir, "config")
	if err := os.MkdirAll(payload, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "seed"), []byte("ours"), 0644); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(home, ".config", "existing")
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("occupant content"), 0644); err != nil {
		t.Fatal(err)
	}

	entries := []manifest.Entry{{Name: "config", Dest: dest}}
	if err := manifest.Write(nsDir, manifest.Manifest{Entries: entries}); err != nil {
		t.Fatal(err)
	}

	problems, err := RestorePreflight(nsDir, "editors", entries)
	if err != nil {
		t.Fatalf("RestorePreflight: %v", err)
	}
	if len(problems) != 1 {
		t.Fatalf("expected exactly one occupied problem, got %+v", problems)
	}

	key := state.Key{Repo: "dotfiles", Namespace: "editors"}
	s := state.State{Entries: map[state.Key]state.Entry{}}
	if err := Restore(key, nsDir, entries, problems, s, false); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	info, err := os.Lstat(dest)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected a real directory (our restored payload) at %s: %v", dest, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "seed")); err != nil {
		t.Fatalf("expected our payload's contents at the destination: %v", err)
	}
}

func TestRestore_Purge_TrashesPayloadAndLeavesDestinationUntouched(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	home := t.TempDir()
	nsDir := t.TempDir()
	payload := filepath.Join(nsDir, "config")
	if err := os.MkdirAll(payload, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "seed"), []byte("ours"), 0644); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(home, ".config", "existing")
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("occupant content, untouched"), 0644); err != nil {
		t.Fatal(err)
	}

	entries := []manifest.Entry{{Name: "config", Dest: dest}}
	if err := manifest.Write(nsDir, manifest.Manifest{Entries: entries}); err != nil {
		t.Fatal(err)
	}

	key := state.Key{Repo: "dotfiles", Namespace: "editors"}
	s := state.State{Entries: map[state.Key]state.Entry{}}
	if err := Restore(key, nsDir, entries, nil, s, true); err != nil {
		t.Fatalf("Restore with purge: %v", err)
	}

	if _, err := os.Stat(payload); !os.IsNotExist(err) {
		t.Fatalf("expected the payload gone (trashed), got err=%v", err)
	}
	content, err := os.ReadFile(dest)
	if err != nil || string(content) != "occupant content, untouched" {
		t.Fatalf("expected the destination untouched by purge, got content=%q err=%v", content, err)
	}

	m, err := manifest.Read(nsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entries) != 0 {
		t.Fatalf("expected the manifest entry removed after purge, got %+v", m.Entries)
	}
}

func TestRestore_Purge_ClearsOwnSymlinkLeftFromTracking(t *testing.T) {
	// namespace <ns> add symlinks the destination immediately, independent
	// of enable/disable state (concept.md "Namespace level"). Purging that
	// entry must not leave that symlink dangling once its target is trashed.
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	home := t.TempDir()
	nsDir := t.TempDir()
	payload := filepath.Join(nsDir, "config")
	if err := os.MkdirAll(payload, 0755); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(home, ".config", "config")
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(payload, dest); err != nil {
		t.Fatal(err)
	}

	entries := []manifest.Entry{{Name: "config", Dest: dest}}
	if err := manifest.Write(nsDir, manifest.Manifest{Entries: entries}); err != nil {
		t.Fatal(err)
	}

	key := state.Key{Repo: "dotfiles", Namespace: "editors"}
	s := state.State{Entries: map[state.Key]state.Entry{}}
	if err := Restore(key, nsDir, entries, nil, s, true); err != nil {
		t.Fatalf("Restore with purge: %v", err)
	}

	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatalf("expected no dangling symlink left at %s after purge, got err=%v", dest, err)
	}
}

func TestRestore_InjectedFailureOnSeventhRestore_RollsBackEverything(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits behave differently on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses permission checks")
	}

	home := t.TempDir()
	nsDir := t.TempDir()

	const total = 10
	const failAt = 7 // 1-indexed: the 7th entry's restore must fail.

	var entries []manifest.Entry
	var blockedDir string
	for i := 1; i <= total; i++ {
		name := fmt.Sprintf("entry%02d", i)
		payload := filepath.Join(nsDir, name)
		if err := os.MkdirAll(payload, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(payload, "seed"), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}

		destDir := filepath.Join(home, fmt.Sprintf("dir%02d", i))
		if i == failAt {
			blockedDir = destDir
		}
		if err := os.MkdirAll(destDir, 0755); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, manifest.Entry{Name: name, Dest: filepath.Join(destDir, "target")})
	}
	if err := manifest.Write(nsDir, manifest.Manifest{Entries: entries}); err != nil {
		t.Fatal(err)
	}

	// Lock the seventh entry's destination parent after creating it, so the
	// directory already satisfies MkdirAll but the rename that moves the
	// payload in fails with a real permission error.
	if err := os.Chmod(blockedDir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(blockedDir, 0755) })

	key := state.Key{Repo: "dotfiles", Namespace: "editors"}
	s := state.State{Entries: map[state.Key]state.Entry{}}

	problems, err := RestorePreflight(nsDir, "editors", entries)
	if err != nil {
		t.Fatalf("RestorePreflight: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("expected no occupied-destination problems, got %+v", problems)
	}

	if err := Restore(key, nsDir, entries, problems, s, false); err == nil {
		t.Fatal("expected the seventh restore's permission failure to surface as an error")
	}

	for i, e := range entries {
		payload := filepath.Join(nsDir, e.Name)
		if i+1 == failAt {
			continue
		}
		if _, err := os.Stat(payload); err != nil {
			t.Fatalf("expected entry %d's payload back in the namespace, got err=%v", i+1, err)
		}
		if _, err := os.Lstat(e.Dest); !os.IsNotExist(err) {
			t.Fatalf("expected entry %d's destination empty again after rollback, got err=%v", i+1, err)
		}
	}

	m, err := manifest.Read(nsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entries) != total {
		t.Fatalf("expected the manifest untouched after a rolled-back restore, got %d entries", len(m.Entries))
	}
}
