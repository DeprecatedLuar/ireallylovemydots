package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
)

// registerRepoWithNamespace registers one repository holding one namespace
// materialized directly on disk (as namespace add / namespace <ns> add
// would leave it), with the given entries already written into its
// manifest but not necessarily linked.
func registerRepoWithNamespace(t *testing.T, nsName string, entries []manifest.Entry) (dataDir, repoDir, nsDir string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	reg := manifest.Registry{Repos: []manifest.Repo{
		{Name: "dotfiles", Owner: "someone", URL: "https://example.com/someone/dotfiles"},
	}}
	if err := manifest.WriteRegistry(reg); err != nil {
		t.Fatal(err)
	}

	dataDir = filepath.Join(dataHome, "ireallylovemydots")
	repoDir = filepath.Join(dataDir, "dotfiles")
	nsDir = filepath.Join(repoDir, nsName)
	if err := os.MkdirAll(nsDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name == "" {
			continue
		}
		if err := os.MkdirAll(filepath.Join(nsDir, e.Name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := manifest.Write(nsDir, manifest.Manifest{Entries: entries}); err != nil {
		t.Fatal(err)
	}
	return dataDir, repoDir, nsDir
}

func TestEnableNamespace_NonInteractive_PromptsOnceListsEveryOccupiedDestination(t *testing.T) {
	home := t.TempDir()

	var entries []manifest.Entry
	var occupiedDests []string
	for i := 0; i < 4; i++ {
		name := "occ" + strconv.Itoa(i)
		dest := filepath.Join(home, name)
		if err := os.WriteFile(dest, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, manifest.Entry{Name: name, Dest: dest})
		occupiedDests = append(occupiedDests, dest)
	}
	for i := 0; i < 6; i++ {
		name := "clean" + strconv.Itoa(i)
		entries = append(entries, manifest.Entry{Name: name, Dest: filepath.Join(home, name)})
	}

	registerRepoWithNamespace(t, "editors", entries)

	err := enableNamespace("editors", shared.Flags{})
	if err == nil {
		t.Fatal("expected a non-interactive enable with occupied destinations and no --force to fail")
	}
	for _, dest := range occupiedDests {
		if !strings.Contains(err.Error(), dest) {
			t.Fatalf("expected the single error to name every occupied destination, missing %s in: %v", dest, err)
		}
	}

	for _, dest := range occupiedDests {
		info, statErr := os.Lstat(dest)
		if statErr != nil {
			t.Fatalf("expected the occupied destination to remain on disk: %v", statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("expected no link created at %s when the prompt was never confirmed", dest)
		}
	}
}

func TestEnableNamespace_OccupiedMessageNamesForceAndTracking(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, "ssh")
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "config"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	entries := []manifest.Entry{{Name: "ssh", Dest: dest}}
	registerRepoWithNamespace(t, "editors", entries)

	err := enableNamespace("editors", shared.Flags{})
	if err == nil {
		t.Fatal("expected an occupied destination to block enable")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected the error to name --force, got: %v", err)
	}
	if !strings.Contains(err.Error(), "track the paths inside it") {
		t.Fatalf("expected the error to name tracking the paths inside it instead of the parent, got: %v", err)
	}
}

func TestEnableNamespace_Force_TrashesOccupiedDestinationAndLinks(t *testing.T) {
	home := t.TempDir()

	dest := filepath.Join(home, "existing")
	entries := []manifest.Entry{{Name: "existing", Dest: dest}}
	_, _, nsDir := registerRepoWithNamespace(t, "editors", entries)
	if err := os.WriteFile(dest, []byte("old content"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := enableNamespace("editors", shared.Flags{Force: true}); err != nil {
		t.Fatalf("enableNamespace with --force: %v", err)
	}

	info, err := os.Lstat(dest)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected the occupied destination replaced by a symlink: %v", err)
	}
	target, err := os.Readlink(dest)
	if err != nil || target != filepath.Join(nsDir, "existing") {
		t.Fatalf("expected the symlink to point at the namespace payload, got %s (err=%v)", target, err)
	}
}

func TestEnableNamespace_LinkGuard_CreatesNothingInDataDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	reg := manifest.Registry{Repos: []manifest.Repo{
		{Name: "dotfiles", Owner: "someone", URL: "https://example.com/someone/dotfiles"},
	}}
	if err := manifest.WriteRegistry(reg); err != nil {
		t.Fatal(err)
	}

	repoDir := filepath.Join(dataHome, "ireallylovemydots", "dotfiles")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")

	nvimDest := filepath.Join(home, ".config", "nvim")
	initLua := filepath.Join(nvimDest, "init.lua")
	nsDir := filepath.Join(repoDir, "editors")
	if err := os.MkdirAll(filepath.Join(nsDir, "nvim"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Write(nsDir, manifest.Manifest{Entries: []manifest.Entry{
		{Name: "nvim", Dest: nvimDest},
		{Name: "init.lua", Dest: initLua},
	}}); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")

	// Simulate a blobless-no-checkout clone: the namespace was never
	// materialized locally.
	if err := os.RemoveAll(nsDir); err != nil {
		t.Fatal(err)
	}

	err := enableNamespace("editors", shared.Flags{Force: true})
	if err == nil {
		t.Fatal("expected the in-repo link guard to block enable even with --force")
	}

	if _, statErr := os.Stat(nsDir); !os.IsNotExist(statErr) {
		t.Fatalf("expected the guard failure to leave the namespace unmaterialized, got err=%v", statErr)
	}
}
