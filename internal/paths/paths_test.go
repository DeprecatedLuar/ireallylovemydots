package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func withXDGData(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", dir)
}

func TestInsideDataDir_Direct(t *testing.T) {
	base := t.TempDir()
	withXDGData(t, base)

	dataDir, err := Data()
	if err != nil {
		t.Fatalf("Data() error: %v", err)
	}

	target := filepath.Join(dataDir, "repo", "namespace")
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}

	inside, err := InsideDataDir(target)
	if err != nil {
		t.Fatalf("InsideDataDir error: %v", err)
	}
	if !inside {
		t.Fatalf("expected %s to be inside data dir %s", target, dataDir)
	}
}

func TestInsideDataDir_Outside(t *testing.T) {
	base := t.TempDir()
	withXDGData(t, base)

	outside := t.TempDir()
	inside, err := InsideDataDir(outside)
	if err != nil {
		t.Fatalf("InsideDataDir error: %v", err)
	}
	if inside {
		t.Fatalf("expected %s to be outside data dir", outside)
	}
}

func TestInsideDataDir_ThroughIntermediateSymlink(t *testing.T) {
	base := t.TempDir()
	withXDGData(t, base)

	dataDir, err := Data()
	if err != nil {
		t.Fatalf("Data() error: %v", err)
	}

	// A destination outside the data dir whose parent is actually a symlink
	// pointing back inside it must still be caught.
	outsideParent := filepath.Join(base, "elsewhere")
	if err := os.MkdirAll(filepath.Dir(outsideParent), 0700); err != nil {
		t.Fatal(err)
	}
	realDir := filepath.Join(dataDir, "repo", "namespace", "nvim")
	if err := os.MkdirAll(realDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDir, outsideParent); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(outsideParent, "init.lua")
	inside, err := InsideDataDir(target)
	if err != nil {
		t.Fatalf("InsideDataDir error: %v", err)
	}
	if !inside {
		t.Fatalf("expected %s (through symlink) to be detected inside data dir", target)
	}
}
