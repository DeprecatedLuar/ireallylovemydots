package commands

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/repo"
)

// newSourceRepo builds a git repository at a fresh temp dir, running each
// git subcommand given in commands (e.g. []string{"init", "-b", "main"}).
func newSourceRepo(t *testing.T, commands ...[]string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	for _, c := range commands {
		run(c...)
	}
	return dir
}

func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	f()

	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestAddRepo_IncompatibleLeavesNoCloneAndRegistryUntouched(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)

	source := newSourceRepo(t)
	if err := os.MkdirAll(filepath.Join(source, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "src", "main.go"), []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = source
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("add", ".")
	run("commit", "-m", "init")

	if err := addRepo("file://"+source, shared.Flags{}); err == nil {
		t.Fatal("expected error registering an incompatible repository")
	}

	reg, err := manifest.ReadRegistry()
	if err != nil {
		t.Fatalf("ReadRegistry: %v", err)
	}
	if len(reg.Repos) != 0 {
		t.Fatalf("registry should be untouched, got %+v", reg.Repos)
	}

	entries, err := os.ReadDir(filepath.Join(dataHome, "ireallylovemydots"))
	if err != nil {
		t.Fatalf("read data dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no clone left in the data directory, got %+v", entries)
	}
}

func TestAddRepo_EmptyRegistersSilently(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	source := newSourceRepo(t)

	out := captureStdout(t, func() {
		if err := addRepo("file://"+source, shared.Flags{}); err != nil {
			t.Fatalf("addRepo: %v", err)
		}
	})
	if out != "" {
		t.Fatalf("expected no output registering an empty repository, got %q", out)
	}

	reg, err := manifest.ReadRegistry()
	if err != nil {
		t.Fatalf("ReadRegistry: %v", err)
	}
	if len(reg.Repos) != 1 {
		t.Fatalf("expected the empty repository to be registered, got %+v", reg.Repos)
	}
}

func TestAddRepo_NamespacesRegisters(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	source := newSourceRepo(t)
	if err := os.MkdirAll(filepath.Join(source, "editors"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "editors", ".dots"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = source
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("add", ".")
	run("commit", "-m", "init")

	if err := addRepo("file://"+source, shared.Flags{}); err != nil {
		t.Fatalf("addRepo: %v", err)
	}

	reg, err := manifest.ReadRegistry()
	if err != nil {
		t.Fatalf("ReadRegistry: %v", err)
	}
	if len(reg.Repos) != 1 {
		t.Fatalf("expected the repository to be registered, got %+v", reg.Repos)
	}
}

func TestAddRepo_RejectsReservedDerivedName(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	if err := addRepo("https://example.com/owner/list.git", shared.Flags{}); err == nil {
		t.Fatal("expected error registering a repository named \"list\"")
	}

	reg, err := manifest.ReadRegistry()
	if err != nil {
		t.Fatalf("ReadRegistry: %v", err)
	}
	if len(reg.Repos) != 0 {
		t.Fatalf("registry should be untouched, got %+v", reg.Repos)
	}
}

func TestInitRepo_IncompatibleLeavesSourceAndRegistryUntouched(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "src", "main.go"), []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	beforeRegistry, err := os.ReadFile(registryPathForTest(t))
	hadRegistry := err == nil

	if err := initRepo(src, shared.Flags{}); err == nil {
		t.Fatal("expected error initializing an incompatible folder")
	}

	if _, err := os.Stat(filepath.Join(src, ".git")); !os.IsNotExist(err) {
		t.Fatalf("expected no .git created in the refused folder, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(src, "src", "main.go")); err != nil {
		t.Fatalf("expected the folder left exactly as found: %v", err)
	}

	reg, err := manifest.ReadRegistry()
	if err != nil {
		t.Fatalf("ReadRegistry: %v", err)
	}
	if len(reg.Repos) != 0 {
		t.Fatalf("registry should be untouched, got %+v", reg.Repos)
	}
	if hadRegistry {
		afterRegistry, err := os.ReadFile(registryPathForTest(t))
		if err != nil {
			t.Fatalf("read registry: %v", err)
		}
		if string(beforeRegistry) != string(afterRegistry) {
			t.Fatal("registry file changed despite the refused init")
		}
	}
}

func TestInitRepo_BootstrapNonInteractiveErrorsAndConvertsNothing(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "nvim"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "nvim", "init.lua"), []byte("-- config"), 0644); err != nil {
		t.Fatal(err)
	}

	err := initRepo(src, shared.Flags{Bootstrap: true})
	if err == nil {
		t.Fatal("expected --bootstrap to error non-interactively rather than convert silently")
	}
	if _, err := os.Stat(filepath.Join(src, ".git")); !os.IsNotExist(err) {
		t.Fatalf("expected no .git created, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(src, "nvim", "init.lua")); err != nil {
		t.Fatalf("expected the folder left exactly as found: %v", err)
	}
	reg, err := manifest.ReadRegistry()
	if err != nil {
		t.Fatalf("ReadRegistry: %v", err)
	}
	if len(reg.Repos) != 0 {
		t.Fatalf("registry should be untouched, got %+v", reg.Repos)
	}
}

// Faking the y/N prompt itself needs a real pty, which this package's tests
// do not set up (see internal/ui/ui_test.go) — the confirmed and declined
// interactive paths of repo add/init --bootstrap are exercised by the
// manual smoke test instead. What is covered here headlessly: the plan and
// apply mechanics (internal/repo/bootstrap_test.go), the non-interactive
// hard error above, and — below — that a bootstrap-converted namespace
// actually enables through the real Phase 5 path, proving Apply's output is
// genuinely usable and not just structurally plausible.
func TestBootstrap_ConvertedNamespaceEnablesThroughRealPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)

	dataDir := filepath.Join(dataHome, "ireallylovemydots")
	repoDir := filepath.Join(dataDir, "dotfiles")
	if err := os.MkdirAll(filepath.Join(repoDir, "nvim"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "nvim", "init.lua"), []byte("-- config"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".bashrc"), []byte("# bashrc"), 0644); err != nil {
		t.Fatal(err)
	}

	preEntries, err := repo.DiskEntries(repoDir)
	if err != nil {
		t.Fatalf("DiskEntries: %v", err)
	}
	plan, err := repo.PlanEntries(preEntries)
	if err != nil {
		t.Fatalf("PlanEntries: %v", err)
	}
	if err := repo.Apply(repoDir, plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	postEntries, err := repo.DiskEntries(repoDir)
	if err != nil {
		t.Fatalf("DiskEntries: %v", err)
	}
	if state := repo.Inspect(postEntries); state != repo.StateNamespaces {
		t.Fatalf("expected the converted repository to pass the compatibility check as namespaces, got state=%v", state)
	}

	reg := manifest.Registry{Repos: []manifest.Repo{{Name: "dotfiles"}}}
	if err := manifest.WriteRegistry(reg); err != nil {
		t.Fatalf("WriteRegistry: %v", err)
	}

	if err := enableNamespace("nvim", shared.Flags{}); err != nil {
		t.Fatalf("enableNamespace: %v", err)
	}

	linkTarget, err := os.Readlink(filepath.Join(home, ".config", "nvim"))
	if err != nil {
		t.Fatalf("expected nvim linked at ~/.config/nvim: %v", err)
	}
	if linkTarget != filepath.Join(repoDir, "nvim", "nvim") {
		t.Fatalf("link target = %q, want %q", linkTarget, filepath.Join(repoDir, "nvim", "nvim"))
	}
}

func TestInitRepo_EmptyFolderRegistersSilentlyAndMoves(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)

	parent := t.TempDir()
	src := filepath.Join(parent, "freshdots")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := initRepo(src, shared.Flags{}); err != nil {
			t.Fatalf("initRepo: %v", err)
		}
	})
	if out != "" {
		t.Fatalf("expected no output registering an empty folder, got %q", out)
	}

	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Fatalf("expected nothing left at the source, got err=%v", err)
	}

	reg, err := manifest.ReadRegistry()
	if err != nil {
		t.Fatalf("ReadRegistry: %v", err)
	}
	if len(reg.Repos) != 1 || reg.Repos[0].Name != "freshdots" {
		t.Fatalf("expected freshdots registered, got %+v", reg.Repos)
	}

	if _, err := os.Stat(filepath.Join(dataHome, "ireallylovemydots", "freshdots")); err != nil {
		t.Fatalf("expected the folder present in the data directory: %v", err)
	}
}

func TestInitRepo_AlreadyGitRepoWithOriginPreservesRemote(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)

	parent := t.TempDir()
	src := filepath.Join(parent, "existingrepo")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = src
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("remote", "add", "origin", "file:///nonexistent/upstream")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	if err := os.MkdirAll(filepath.Join(src, "editors"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "editors", ".dots"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")

	if err := initRepo(src, shared.Flags{}); err != nil {
		t.Fatalf("initRepo: %v", err)
	}

	dest := filepath.Join(dataHome, "ireallylovemydots", "existingrepo")
	remoteOut, err := exec.Command("git", "-C", dest, "remote", "-v").CombinedOutput()
	if err != nil {
		t.Fatalf("git remote -v: %v", err)
	}
	if !strings.Contains(string(remoteOut), "file:///nonexistent/upstream") {
		t.Fatalf("expected origin preserved at the new location, got: %s", remoteOut)
	}
}

func TestInitRepo_ActsOnPathArgumentRegardlessOfCwd(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)

	parent := t.TempDir()
	src := filepath.Join(parent, "targetdots")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}

	unrelated := t.TempDir()
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(unrelated); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origWd)

	if err := initRepo(src, shared.Flags{}); err != nil {
		t.Fatalf("initRepo: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dataHome, "ireallylovemydots", "targetdots")); err != nil {
		t.Fatalf("expected the argument path to be taken, not the cwd: %v", err)
	}
}

func TestInitRepo_RefusesPathInsideDataDirectory(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)

	dataDir := filepath.Join(dataHome, "ireallylovemydots")
	inside := filepath.Join(dataDir, "already-there")
	if err := os.MkdirAll(inside, 0755); err != nil {
		t.Fatal(err)
	}

	if err := initRepo(inside, shared.Flags{}); err == nil {
		t.Fatal("expected error initializing a path already inside the data directory")
	}

	entries, err := os.ReadDir(dataDir)
	if err != nil {
		t.Fatalf("read data dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "already-there" {
		t.Fatalf("expected nothing moved, got %+v", entries)
	}
}

func TestInitRepo_CollisionWithReservedWordErrorsNonInteractive(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	parent := t.TempDir()
	src := filepath.Join(parent, "list")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}

	if err := initRepo(src, shared.Flags{}); err == nil {
		t.Fatal("expected error initializing a folder whose derived name is reserved")
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("expected the source left untouched: %v", err)
	}

	reg, err := manifest.ReadRegistry()
	if err != nil {
		t.Fatalf("ReadRegistry: %v", err)
	}
	if len(reg.Repos) != 0 {
		t.Fatalf("registry should be untouched, got %+v", reg.Repos)
	}
}

func TestInitRepo_CollisionWithExistingRepoNameErrorsNonInteractive(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	existing := manifest.Registry{Repos: []manifest.Repo{
		{Name: "dotfiles"},
	}}
	if err := manifest.WriteRegistry(existing); err != nil {
		t.Fatalf("WriteRegistry: %v", err)
	}

	parent := t.TempDir()
	src := filepath.Join(parent, "dotfiles")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}

	if err := initRepo(src, shared.Flags{}); err == nil {
		t.Fatal("expected error initializing a folder whose derived name collides with a registered repository")
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("expected the source left untouched: %v", err)
	}

	reg, err := manifest.ReadRegistry()
	if err != nil {
		t.Fatalf("ReadRegistry: %v", err)
	}
	if len(reg.Repos) != 1 {
		t.Fatalf("registry should be untouched, got %+v", reg.Repos)
	}
}

func TestRenderRepoList_MissingCloneMarkedProblemRegistryUnchanged(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)

	reg := manifest.Registry{Repos: []manifest.Repo{
		{Name: "dotfiles", Owner: "someone", URL: "https://example.com/someone/dotfiles"},
	}}
	if err := manifest.WriteRegistry(reg); err != nil {
		t.Fatal(err)
	}
	// The clone is never created: this is the "deleted the repository's
	// folder from the data directory" case from concept.md "Repository
	// manifest" — a missing clone must never silently deregister.

	out := captureStdout(t, func() {
		if err := renderRepoList(); err != nil {
			t.Fatalf("renderRepoList: %v", err)
		}
	})
	if !strings.Contains(out, "! dotfiles") {
		t.Fatalf("expected the missing clone marked \"!\", got %q", out)
	}

	after, err := manifest.ReadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Repos) != 1 {
		t.Fatalf("expected the registry left unchanged, got %+v", after.Repos)
	}
}

// registryPathForTest reads the registry path the same way the production
// code does, for byte-identity comparisons in tests.
func registryPathForTest(t *testing.T) string {
	t.Helper()
	p, err := manifest.RegistryPath()
	if err != nil {
		t.Fatalf("RegistryPath: %v", err)
	}
	return p
}

func TestAddRepo_RejectsDuplicateDerivedName_NonInteractive(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	existing := manifest.Registry{Repos: []manifest.Repo{
		{Name: "dotfiles", Owner: "someone", URL: "https://example.com/someone/dotfiles"},
	}}
	if err := manifest.WriteRegistry(existing); err != nil {
		t.Fatalf("WriteRegistry: %v", err)
	}

	if err := addRepo("https://example.com/other/dotfiles.git", shared.Flags{}); err == nil {
		t.Fatal("expected error registering a duplicate name outside a terminal")
	}

	reg, err := manifest.ReadRegistry()
	if err != nil {
		t.Fatalf("ReadRegistry: %v", err)
	}
	if len(reg.Repos) != 1 {
		t.Fatalf("registry should be untouched, got %+v", reg.Repos)
	}
}
