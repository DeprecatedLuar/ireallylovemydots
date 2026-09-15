package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/profile"
	"github.com/DeprecatedLuar/dotz/internal/state"
)

func TestRestoreCopy_EnabledFileAndDirectoryEntries_EndAsRealCopies(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	home := t.TempDir()
	nsDir := t.TempDir()

	filePayload := filepath.Join(nsDir, "gitconfig")
	if err := os.WriteFile(filePayload, []byte("file content"), 0644); err != nil {
		t.Fatal(err)
	}
	dirPayload := filepath.Join(nsDir, "nvim")
	if err := os.MkdirAll(dirPayload, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirPayload, "init.lua"), []byte("dir content"), 0644); err != nil {
		t.Fatal(err)
	}

	destFile := filepath.Join(home, ".gitconfig")
	destDir := filepath.Join(home, ".config", "nvim")
	entries := []manifest.Entry{
		{Name: "gitconfig", Dest: destFile},
		{Name: "nvim", Dest: destDir},
	}

	key := state.Key{Repo: "dotfiles", Namespace: "editors"}
	s := state.State{Entries: map[state.Key]state.Entry{}}
	if _, err := Enable(key, nsDir, nsDir, "editors", entries, s, nil); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	if err := RestoreCopy(key, nsDir, entries, nil, s, false); err != nil {
		t.Fatalf("RestoreCopy: %v", err)
	}

	// Both destinations must now be real copies, not symlinks.
	info, err := os.Lstat(destFile)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected a real file at %s, got err=%v", destFile, err)
	}
	content, err := os.ReadFile(destFile)
	if err != nil || string(content) != "file content" {
		t.Fatalf("expected the payload's content at %s, got %q err=%v", destFile, content, err)
	}

	info, err = os.Lstat(destDir)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected a real directory at %s, got err=%v", destDir, err)
	}
	content, err = os.ReadFile(filepath.Join(destDir, "init.lua"))
	if err != nil || string(content) != "dir content" {
		t.Fatalf("expected the payload's content at %s, got %q err=%v", destDir, content, err)
	}

	// The payloads must still exist in the namespace — RestoreCopy never
	// moves them, unlike Restore (rm --restore).
	if _, err := os.Stat(filePayload); err != nil {
		t.Fatalf("expected the file payload to remain in the namespace: %v", err)
	}
	if _, err := os.Stat(dirPayload); err != nil {
		t.Fatalf("expected the directory payload to remain in the namespace: %v", err)
	}

	entry := s.Entries[key]
	if entry.Enabled {
		t.Fatalf("expected the namespace to end disabled, got Enabled=true")
	}
	if len(entry.LinkedDests) != 0 {
		t.Fatalf("expected no linked destinations, got %+v", entry.LinkedDests)
	}
}

func TestRestoreCopy_ProfiledEntry_CopiesActiveProfileVersion(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	home := t.TempDir()
	nsDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(nsDir, "config"), []byte("main version"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := profile.Write(nsDir, profile.Manifest{Profiles: []string{"work"}, Entries: []string{"config"}}); err != nil {
		t.Fatal(err)
	}
	overrideDir := profile.ProfileDir(nsDir, "work")
	if err := os.MkdirAll(overrideDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(overrideDir, "config"), []byte("work version"), 0644); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(home, ".config")
	entries := []manifest.Entry{{Name: "config", Dest: dest}}

	key := state.Key{Repo: "dotfiles", Namespace: "editors"}
	s := state.State{Entries: map[state.Key]state.Entry{key: {ActiveProfile: "work"}}}

	if err := RestoreCopy(key, nsDir, entries, nil, s, false); err != nil {
		t.Fatalf("RestoreCopy: %v", err)
	}

	content, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read %s: %v", dest, err)
	}
	if string(content) != "work version" {
		t.Fatalf("expected the active profile's version copied, got %q", content)
	}
}

func TestRestoreCopy_OccupiedDestinationSkip_LeftUntouched(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	home := t.TempDir()
	nsDir := t.TempDir()

	payload := filepath.Join(nsDir, "config")
	if err := os.WriteFile(payload, []byte("ours"), 0644); err != nil {
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
	problems, err := RestorePreflight(nsDir, "editors", entries)
	if err != nil {
		t.Fatalf("RestorePreflight: %v", err)
	}
	if len(problems) != 1 {
		t.Fatalf("expected exactly one occupied problem, got %+v", problems)
	}

	key := state.Key{Repo: "dotfiles", Namespace: "editors"}
	s := state.State{Entries: map[state.Key]state.Entry{}}
	if err := RestoreCopy(key, nsDir, entries, problems, s, true); err != nil {
		t.Fatalf("RestoreCopy with skip: %v", err)
	}

	content, err := os.ReadFile(dest)
	if err != nil || string(content) != "occupant content" {
		t.Fatalf("expected the occupant left untouched at %s, got content=%q err=%v", dest, content, err)
	}
	if payloadContent, err := os.ReadFile(payload); err != nil || string(payloadContent) != "ours" {
		t.Fatalf("expected our payload untouched in the namespace, got content=%q err=%v", payloadContent, err)
	}
}

func TestRestoreCopy_FailureOnLastEntry_RollsBackEveryEarlierEntryToSymlink(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	home := t.TempDir()
	nsDir := t.TempDir()

	payload1 := filepath.Join(nsDir, "entry1")
	if err := os.MkdirAll(payload1, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload1, "seed"), []byte("one"), 0644); err != nil {
		t.Fatal(err)
	}
	payload2 := filepath.Join(nsDir, "entry2")
	if err := os.MkdirAll(payload2, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload2, "seed"), []byte("two"), 0644); err != nil {
		t.Fatal(err)
	}

	dest1 := filepath.Join(home, "one")
	dest2 := filepath.Join(home, "two")
	// entry3's payload is deliberately never created, so its copy step fails
	// without anything of its own having been touched first.
	dest3 := filepath.Join(home, "three")

	linked := []manifest.Entry{
		{Name: "entry1", Dest: dest1},
		{Name: "entry2", Dest: dest2},
	}
	entries := append(append([]manifest.Entry{}, linked...), manifest.Entry{Name: "entry3", Dest: dest3})

	key := state.Key{Repo: "dotfiles", Namespace: "editors"}
	s := state.State{Entries: map[state.Key]state.Entry{}}
	if _, err := Enable(key, nsDir, nsDir, "editors", linked, s, nil); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	if err := RestoreCopy(key, nsDir, entries, nil, s, false); err == nil {
		t.Fatal("expected the third entry's missing payload to surface as an error")
	}

	for _, e := range linked {
		info, err := os.Lstat(e.Dest)
		if err != nil {
			t.Fatalf("expected %s restored to a symlink after rollback, got err=%v", e.Dest, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("expected %s to be a symlink after rollback, got a real file/dir", e.Dest)
		}
		target, err := os.Readlink(e.Dest)
		if err != nil {
			t.Fatalf("readlink %s: %v", e.Dest, err)
		}
		want := filepath.Join(nsDir, e.Name)
		if target != want {
			t.Fatalf("expected %s to point at %s, got %s", e.Dest, want, target)
		}
	}

	if _, err := os.Lstat(dest3); !os.IsNotExist(err) {
		t.Fatalf("expected the third destination to remain empty, got err=%v", err)
	}
}
