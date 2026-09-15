package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/state"
)

// setupTwoRepos registers two repositories, "src" and "dst", under a
// sandboxed set of XDG directories, and returns the data directory both live
// under.
func setupTwoRepos(t *testing.T) (dataDir string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	reg := manifest.Registry{Repos: []manifest.Repo{
		{Name: "src", Owner: "someone", URL: "https://example.com/someone/src"},
		{Name: "dst", Owner: "someone", URL: "https://example.com/someone/dst"},
	}}
	if err := manifest.WriteRegistry(reg); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dataHome, "ireallylovemydots")
}

// writeMaterializedNamespace writes a namespace directory directly on disk
// (as `namespace add` would leave it), with a manifest, one payload file,
// and a .profiles/ directory, so tests can assert every one of them survives
// a copy.
func writeMaterializedNamespace(t *testing.T, dataDir, repoName, nsName string) (nsDir string) {
	t.Helper()
	nsDir = filepath.Join(dataDir, repoName, nsName)
	if err := os.MkdirAll(filepath.Join(nsDir, "payload"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nsDir, "payload", "seed"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(nsDir, ".profiles"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nsDir, ".profiles", "work.toml"), []byte("entries = []\n"), 0644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "payload")
	if err := manifest.Write(nsDir, manifest.Manifest{Entries: []manifest.Entry{{Name: "payload", Dest: dest}}}); err != nil {
		t.Fatal(err)
	}
	return nsDir
}

func TestParseNamespaceSpec_OneSlash(t *testing.T) {
	repos := []manifest.Repo{{Name: "dotfiles", Owner: "someone"}}
	r, name, err := parseNamespaceSpec(repos, "dotfiles/nvim")
	if err != nil {
		t.Fatalf("parseNamespaceSpec: %v", err)
	}
	if r.Name != "dotfiles" || name != "nvim" {
		t.Fatalf("got repo=%q name=%q, want dotfiles/nvim", r.Name, name)
	}
}

func TestParseNamespaceSpec_TwoSlashes(t *testing.T) {
	repos := []manifest.Repo{{Name: "dotfiles", Owner: "someone"}}
	r, name, err := parseNamespaceSpec(repos, "someone/dotfiles/nvim")
	if err != nil {
		t.Fatalf("parseNamespaceSpec: %v", err)
	}
	if r.Name != "dotfiles" || name != "nvim" {
		t.Fatalf("got repo=%q name=%q, want dotfiles/nvim", r.Name, name)
	}
}

func TestParseNamespaceSpec_SingleSegment_Errors(t *testing.T) {
	repos := []manifest.Repo{{Name: "dotfiles", Owner: "someone"}}
	_, _, err := parseNamespaceSpec(repos, "nvim")
	if err == nil {
		t.Fatal("expected a single-segment spec to error")
	}
	if !strings.Contains(err.Error(), "repo/namespace") {
		t.Fatalf("expected the error to name the required form, got: %v", err)
	}
}

func TestHandleCp_MaterializedSource_CopiesManifestPayloadAndProfiles(t *testing.T) {
	dataDir := setupTwoRepos(t)
	writeMaterializedNamespace(t, dataDir, "src", "nvim")

	if err := HandleCp([]string{"src/nvim", "dst/nvim"}, shared.Flags{}); err != nil {
		t.Fatalf("cp src/nvim dst/nvim: %v", err)
	}

	dstDir := filepath.Join(dataDir, "dst", "nvim")
	if _, err := os.Stat(filepath.Join(dstDir, "payload", "seed")); err != nil {
		t.Fatalf("expected payload copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dstDir, ".profiles", "work.toml")); err != nil {
		t.Fatalf("expected .profiles/ copied: %v", err)
	}
	m, err := manifest.Read(dstDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entries) != 1 || m.Entries[0].Name != "payload" {
		t.Fatalf("expected the manifest entry copied, got %+v", m.Entries)
	}

	// Source untouched.
	srcDir := filepath.Join(dataDir, "src", "nvim")
	if _, err := os.Stat(filepath.Join(srcDir, "payload", "seed")); err != nil {
		t.Fatalf("expected the source left untouched: %v", err)
	}
}

func TestHandleCp_IgnoreClearedInTheCopy(t *testing.T) {
	dataDir := setupTwoRepos(t)
	nsDir := writeMaterializedNamespace(t, dataDir, "src", "nvim")
	m, err := manifest.Read(nsDir)
	if err != nil {
		t.Fatal(err)
	}
	m.Ignore = true
	if err := manifest.Write(nsDir, m); err != nil {
		t.Fatal(err)
	}

	if err := HandleCp([]string{"src/nvim", "dst/nvim"}, shared.Flags{}); err != nil {
		t.Fatalf("cp src/nvim dst/nvim: %v", err)
	}

	dstM, err := manifest.Read(filepath.Join(dataDir, "dst", "nvim"))
	if err != nil {
		t.Fatal(err)
	}
	if dstM.Ignore {
		t.Fatal("expected Ignore cleared in the copy")
	}

	srcM, err := manifest.Read(nsDir)
	if err != nil {
		t.Fatal(err)
	}
	if !srcM.Ignore {
		t.Fatal("expected the source's Ignore left untouched")
	}
}

func TestHandleCp_NonMaterializedSource_CopiesOutOfGit(t *testing.T) {
	dataDir := setupTwoRepos(t)
	repoDir := filepath.Join(dataDir, "src")
	nsDir := filepath.Join(repoDir, "nvim")
	if err := os.MkdirAll(filepath.Join(nsDir, "payload"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nsDir, "payload", "seed"), []byte("committed"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Write(nsDir, manifest.Manifest{Entries: []manifest.Entry{{Name: "payload", Dest: "~/nowhere"}}}); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoDir, "init", "-b", "main")
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "init")

	// Remove the working copy so the namespace is committed but not
	// materialized — the state a fresh clone of a read-only repository would
	// be in before install.
	if err := os.RemoveAll(nsDir); err != nil {
		t.Fatal(err)
	}

	if err := HandleCp([]string{"src/nvim", "dst/nvim"}, shared.Flags{}); err != nil {
		t.Fatalf("cp src/nvim dst/nvim: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dataDir, "dst", "nvim", "payload", "seed"))
	if err != nil {
		t.Fatalf("expected the payload read out of git: %v", err)
	}
	if string(data) != "committed" {
		t.Fatalf("payload content = %q, want %q", data, "committed")
	}
}

func TestHandleCp_ReadOnlyDestination_Errors(t *testing.T) {
	dataDir := setupTwoRepos(t)
	writeMaterializedNamespace(t, dataDir, "src", "nvim")

	access := state.Access{}
	access.SetReadOnly("dst", true)
	if err := state.WriteAccess(access); err != nil {
		t.Fatal(err)
	}

	err := HandleCp([]string{"src/nvim", "dst/nvim"}, shared.Flags{})
	if err == nil {
		t.Fatal("expected cp into a read-only destination to error")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("expected the error to name read-only, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "dst", "nvim")); !os.IsNotExist(err) {
		t.Fatalf("expected nothing written to the destination, got err=%v", err)
	}
}

func TestHandleCp_ExistingDestinationNamespace_Errors(t *testing.T) {
	dataDir := setupTwoRepos(t)
	writeMaterializedNamespace(t, dataDir, "src", "nvim")
	writeMaterializedNamespace(t, dataDir, "dst", "nvim")

	err := HandleCp([]string{"src/nvim", "dst/nvim"}, shared.Flags{})
	if err == nil {
		t.Fatal("expected cp onto an existing destination namespace to error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected the error to name the collision, got: %v", err)
	}
}
