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

func TestIsProtectedRoot_Home(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	protected, err := IsProtectedRoot(home)
	if err != nil {
		t.Fatalf("IsProtectedRoot error: %v", err)
	}
	if !protected {
		t.Fatalf("expected home directory %s to be a protected root", home)
	}
}

func TestIsProtectedRoot_Slash(t *testing.T) {
	protected, err := IsProtectedRoot("/")
	if err != nil {
		t.Fatalf("IsProtectedRoot error: %v", err)
	}
	if !protected {
		t.Fatal("expected / to be a protected root")
	}
}

func TestIsProtectedRoot_XDGConfigRoot(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	root, err := Config()
	if err != nil {
		t.Fatal(err)
	}
	protected, err := IsProtectedRoot(root)
	if err != nil {
		t.Fatalf("IsProtectedRoot error: %v", err)
	}
	if !protected {
		t.Fatalf("expected XDG config root %s to be protected", root)
	}
}

func TestIsProtectedRoot_BeneathRootIsOrdinary(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	root, err := Config()
	if err != nil {
		t.Fatal(err)
	}
	beneath := filepath.Join(root, "nvim")
	protected, err := IsProtectedRoot(beneath)
	if err != nil {
		t.Fatalf("IsProtectedRoot error: %v", err)
	}
	if protected {
		t.Fatalf("expected %s (beneath the root, not the root itself) to be unrestricted", beneath)
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

func TestDataDirLinks_ParentLinkedIntoDataDir(t *testing.T) {
	withXDGData(t, t.TempDir())
	dataDir, _ := Data()
	target := filepath.Join(dataDir, "repo", "ns")
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	linkPath := filepath.Join(home, "x")
	if err := os.Symlink(target, linkPath); err != nil {
		t.Fatal(err)
	}

	links, err := DataDirLinks(filepath.Join(linkPath, "f"))
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].Path != linkPath || links[0].Rel != filepath.Join("repo", "ns") {
		t.Fatalf("unexpected links: %+v", links)
	}
}

func TestDataDirLinks_TwoLinkedAncestorsNearestFirst(t *testing.T) {
	withXDGData(t, t.TempDir())
	dataDir, _ := Data()
	inner := filepath.Join(dataDir, "repo", "a", "inner")
	if err := os.MkdirAll(inner, 0700); err != nil {
		t.Fatal(err)
	}
	outer := filepath.Join(dataDir, "repo", "c")
	if err := os.MkdirAll(outer, 0700); err != nil {
		t.Fatal(err)
	}
	// outer/sub -> data/repo/a (nearest link is reached via a link chain)
	if err := os.Symlink(filepath.Join(dataDir, "repo", "a"), filepath.Join(outer, "sub")); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	top := filepath.Join(home, "top")
	if err := os.Symlink(outer, top); err != nil {
		t.Fatal(err)
	}

	links, err := DataDirLinks(filepath.Join(top, "sub", "inner", "f"))
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 2 {
		t.Fatalf("expected two links, got %+v", links)
	}
	if links[0].Path != filepath.Join(top, "sub") || links[0].Rel != filepath.Join("repo", "a") {
		t.Fatalf("nearest link wrong: %+v", links[0])
	}
	if links[1].Path != top || links[1].Rel != filepath.Join("repo", "c") {
		t.Fatalf("outer link wrong: %+v", links[1])
	}
}

func TestDataDirLinks_ParentLinkedOutsideIsIgnored(t *testing.T) {
	withXDGData(t, t.TempDir())
	outside := t.TempDir()
	linkPath := filepath.Join(t.TempDir(), "x")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Fatal(err)
	}
	links, err := DataDirLinks(filepath.Join(linkPath, "f"))
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 0 {
		t.Fatalf("expected none, got %+v", links)
	}
}

func TestDataDirLinks_NoSymlink(t *testing.T) {
	withXDGData(t, t.TempDir())
	links, err := DataDirLinks(filepath.Join(t.TempDir(), "missing", "f"))
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 0 {
		t.Fatalf("expected none, got %+v", links)
	}
}
