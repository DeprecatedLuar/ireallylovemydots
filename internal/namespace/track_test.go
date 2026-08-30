package namespace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/manifest"
)

func TestAdd_File(t *testing.T) {
	repoDir := t.TempDir()
	nsDir, err := Create(repoDir, "editors")
	if err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(configDir, "init.lua")
	if err := os.WriteFile(target, []byte("-- config"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := Add(nsDir, target); err != nil {
		t.Fatalf("Add: %v", err)
	}

	info, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("expected symlink at original location: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected a symlink left at the original location")
	}

	payload := filepath.Join(nsDir, "init.lua")
	if _, err := os.Stat(payload); err != nil {
		t.Fatalf("expected payload moved into namespace: %v", err)
	}
	// Parent directory stays real and untouched for a file entry.
	if fi, err := os.Lstat(configDir); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected parent directory to remain a real directory")
	}

	m, err := manifest.Read(nsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entries) != 1 || m.Entries[0].Name != "init.lua" || m.Entries[0].Dest != target {
		t.Fatalf("manifest entries = %+v, want one entry for %s", m.Entries, target)
	}
}

func TestAdd_Directory(t *testing.T) {
	repoDir := t.TempDir()
	nsDir, err := Create(repoDir, "editors")
	if err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	target := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}

	if err := Add(nsDir, target); err != nil {
		t.Fatalf("Add: %v", err)
	}

	info, err := os.Lstat(target)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected a symlink left at %s", target)
	}
}

func TestAdd_AdoptsUntrackedPayload(t *testing.T) {
	repoDir := t.TempDir()
	nsDir, err := Create(repoDir, "editors")
	if err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	dest := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		t.Fatal(err)
	}

	// The payload already sits in the namespace, unmanifested — e.g. from
	// sync or a half-finished bootstrap. dest itself does not exist yet.
	payload := filepath.Join(nsDir, "nvim")
	if err := os.MkdirAll(payload, 0755); err != nil {
		t.Fatal(err)
	}

	if err := Add(nsDir, dest); err != nil {
		t.Fatalf("Add (adopt): %v", err)
	}

	info, err := os.Lstat(dest)
	if err != nil {
		t.Fatalf("expected a symlink created at %s: %v", dest, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected %s to be a symlink", dest)
	}
	target, err := os.Readlink(dest)
	if err != nil {
		t.Fatal(err)
	}
	if target != payload {
		t.Fatalf("symlink target = %s, want %s", target, payload)
	}

	m, err := manifest.Read(nsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entries) != 1 || m.Entries[0].Name != "nvim" || m.Entries[0].Dest != dest {
		t.Fatalf("manifest entries = %+v, want one entry for %s", m.Entries, dest)
	}
}

func TestAdd_AlreadyTrackedReportsAndStops(t *testing.T) {
	repoDir := t.TempDir()
	nsDir, err := Create(repoDir, "editors")
	if err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	target := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}

	if err := Add(nsDir, target); err != nil {
		t.Fatalf("Add (first): %v", err)
	}
	if err := Add(nsDir, target); err != nil {
		t.Fatalf("Add (second, already tracked) should not error: %v", err)
	}

	m, err := manifest.Read(nsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entries) != 1 {
		t.Fatalf("expected exactly one manifest entry after tracking twice, got %+v", m.Entries)
	}
}

func TestAdd_RefusesProtectedRoot(t *testing.T) {
	repoDir := t.TempDir()
	nsDir, err := Create(repoDir, "editors")
	if err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := Add(nsDir, home); err == nil {
		t.Fatal("expected error tracking the home directory itself")
	}

	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	configRoot := filepath.Join(configHome, "ireallylovemydots")
	if err := os.MkdirAll(configRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if err := Add(nsDir, configRoot); err == nil {
		t.Fatal("expected error tracking the XDG config root itself")
	}
}

func TestAdd_BasenameCollision(t *testing.T) {
	repoDir := t.TempDir()
	nsDir, err := Create(repoDir, "shells")
	if err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	kittyConfig := filepath.Join(home, ".config", "kitty", "config")
	footConfig := filepath.Join(home, ".config", "foot", "config")
	for _, p := range []string{kittyConfig, footConfig} {
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := Add(nsDir, kittyConfig); err != nil {
		t.Fatalf("Add first entry: %v", err)
	}
	err = Add(nsDir, footConfig)
	if err == nil {
		t.Fatal("expected basename collision error tracking a second \"config\" file")
	}
	if !strings.Contains(err.Error(), kittyConfig) || !strings.Contains(err.Error(), footConfig) {
		t.Fatalf("expected error naming both paths, got: %v", err)
	}
}
