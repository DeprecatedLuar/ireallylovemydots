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
