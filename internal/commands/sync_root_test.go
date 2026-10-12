package commands

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeprecatedLuar/ireallylovemydots/internal/commands/shared"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/manifest"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/repo"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/state"
)

var rootKey = state.Key{Repo: "dotfiles", Namespace: "nvim"}

// newRootSyncRepo builds, pushes, sparsifies, and registers a repository
// whose root namespace "nvim" holds init.lua and README.md beside folder
// namespace "shell".
func newRootSyncRepo(t *testing.T, dataDir, scratchRoot string) (repoDir, remote string) {
	t.Helper()
	remote = newBareRemote(t, scratchRoot, "dotfiles")
	repoDir = filepath.Join(dataDir, "dotfiles")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}
	syncGitRun(t, repoDir, "init", "-b", "main")
	syncGitRun(t, repoDir, "config", "user.name", "dots test")
	syncGitRun(t, repoDir, "config", "user.email", "dots@example.invalid")
	syncGitRun(t, repoDir, "remote", "add", "origin", remote)
	writeTestFile(t, filepath.Join(repoDir, "init.lua"), "seed")
	writeTestFile(t, filepath.Join(repoDir, "README.md"), "seed")
	if err := manifest.Write(repoDir, manifest.Manifest{Name: "nvim"}); err != nil {
		t.Fatal(err)
	}
	writeSyncNamespace(t, repoDir, "shell")
	syncGitRun(t, repoDir, "add", "-A")
	syncGitRun(t, repoDir, "commit", "-m", "seed")
	syncGitRun(t, repoDir, "push", "-u", "origin", "main")
	if err := repo.EnsureSparse(repoDir, []string{"shell"}); err != nil {
		t.Fatal(err)
	}
	reg := manifest.Registry{Repos: []manifest.Repo{{Name: "dotfiles", Owner: "someone", URL: remote}}}
	if err := manifest.WriteRegistry(reg); err != nil {
		t.Fatal(err)
	}
	return repoDir, remote
}

// peerCommit clones remote, lets change edit the clone, then commits and
// pushes whatever it changed.
func peerCommit(t *testing.T, scratchRoot, remote string, change func(peer string)) {
	t.Helper()
	peer := filepath.Join(scratchRoot, "peer-root")
	syncGitRun(t, scratchRoot, "clone", remote, peer)
	syncGitRun(t, peer, "config", "user.name", "peer")
	syncGitRun(t, peer, "config", "user.email", "peer@example.invalid")
	change(peer)
	syncGitRun(t, peer, "add", "-A")
	syncGitRun(t, peer, "commit", "-m", "peer change")
	syncGitRun(t, peer, "push", "origin", "main")
}

func enableRootInState(t *testing.T) {
	t.Helper()
	if err := state.Write(state.State{Entries: map[state.Key]state.Entry{rootKey: {Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
}

// assertNothingTrashed fails when the trash holds anything.
func assertNothingTrashed(t *testing.T) {
	t.Helper()
	trashed, err := os.ReadDir(filepath.Join(os.Getenv("XDG_DATA_HOME"), "Trash", "files"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(trashed) > 0 {
		t.Fatalf("expected nothing trashed, found %d entries", len(trashed))
	}
}

// A loose root file conflicting with the remote is held with the root
// namespace instead of failing the repository.
func TestSync_RootNamespaceConflictIsHeld(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	repoDir, remote := newRootSyncRepo(t, dataDir, scratchRoot)
	pushPeerEdit(t, scratchRoot, remote, "init.lua", "peer")
	writeTestFile(t, filepath.Join(repoDir, "init.lua"), "local")

	var err error
	stdout, _ := captureStdoutStderr(t, func() { err = HandleSync(nil, shared.Flags{}) })
	if !errors.Is(err, ErrSomeSkipped) {
		t.Fatalf("err = %v, want ErrSomeSkipped for a held root namespace", err)
	}
	if !strings.Contains(stdout, "nvim") || strings.Contains(stdout, "outside any namespace") {
		t.Fatalf("summary should hold nvim, not fail the repository:\n%s", stdout)
	}
	if got := readFile(t, filepath.Join(repoDir, "init.lua")); got != "local" {
		t.Fatalf("held init.lua = %q, want untouched", got)
	}
	s, err := state.Read()
	if err != nil {
		t.Fatal(err)
	}
	if s.Entries[rootKey].HeldBase == "" {
		t.Fatal("expected a held base recorded for the root namespace")
	}
}

// Every root unit takes the root namespace's mode.
func TestSync_RootNamespaceOverwriteLocalTakesRemoteForAllRootUnits(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	repoDir, remote := newRootSyncRepo(t, dataDir, scratchRoot)
	pushPeerEdit(t, scratchRoot, remote, "init.lua", "peer")
	writeTestFile(t, filepath.Join(repoDir, "init.lua"), "local")
	writeTestFile(t, filepath.Join(repoDir, "README.md"), "local readme")

	var err error
	captureStdoutStderr(t, func() { err = HandleSync([]string{"nvim"}, shared.Flags{SyncMode: "overwrite-local"}) })
	if err != nil {
		t.Fatalf("HandleSync: %v", err)
	}
	if got := readFile(t, filepath.Join(repoDir, "init.lua")); got != "peer" {
		t.Fatalf("init.lua = %q, want the remote's", got)
	}
	if got := readFile(t, filepath.Join(repoDir, "README.md")); got != "seed" {
		t.Fatalf("README.md = %q, want the remote's: the root namespace syncs as one unit", got)
	}
}

// Deleting the root .dots upstream forgets the namespace and trashes nothing.
func TestSync_RootDotsDeletedUpstreamForgetsWithoutTrash(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	repoDir, remote := newRootSyncRepo(t, dataDir, scratchRoot)
	enableRootInState(t)
	peerCommit(t, scratchRoot, remote, func(peer string) {
		syncGitRun(t, peer, "rm", "-q", ".dots")
	})

	var err error
	captureStdoutStderr(t, func() { err = HandleSync(nil, shared.Flags{Yes: true}) })
	if err != nil {
		t.Fatalf("HandleSync: %v", err)
	}
	s, err := state.Read()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Entries[rootKey]; ok {
		t.Fatal("root namespace still in state after its .dots was deleted upstream")
	}
	if _, err := os.Stat(filepath.Join(repoDir, "init.lua")); err != nil {
		t.Fatalf("init.lua must stay as ordinary repository content: %v", err)
	}
	assertNothingTrashed(t)
}

// A changed name upstream forgets the old name.
func TestSync_RootRenamedUpstreamForgetsOldName(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	repoDir, remote := newRootSyncRepo(t, dataDir, scratchRoot)
	enableRootInState(t)
	peerCommit(t, scratchRoot, remote, func(peer string) {
		if err := manifest.Write(peer, manifest.Manifest{Name: "editor"}); err != nil {
			t.Fatal(err)
		}
	})

	var err error
	captureStdoutStderr(t, func() { err = HandleSync(nil, shared.Flags{Yes: true}) })
	if err != nil {
		t.Fatalf("HandleSync: %v", err)
	}
	s, err := state.Read()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Entries[rootKey]; ok {
		t.Fatal("old root name still in state after an upstream rename")
	}
	if _, err := os.Stat(filepath.Join(repoDir, "init.lua")); err != nil {
		t.Fatalf("init.lua must stay as ordinary repository content: %v", err)
	}
	assertNothingTrashed(t)
}

// Edits under a root namespace deleted upstream go to the trash before the
// remote's version replaces them.
func TestSync_RootDotsDeletedUpstreamTrashesLocalEdits(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	repoDir, remote := newRootSyncRepo(t, dataDir, scratchRoot)
	enableRootInState(t)
	peerCommit(t, scratchRoot, remote, func(peer string) {
		syncGitRun(t, peer, "rm", "-q", ".dots")
	})
	writeTestFile(t, filepath.Join(repoDir, "README.md"), "local readme")

	var err error
	captureStdoutStderr(t, func() { err = HandleSync(nil, shared.Flags{Yes: true}) })
	if err != nil {
		t.Fatalf("HandleSync: %v", err)
	}
	if got := readFile(t, filepath.Join(repoDir, "README.md")); got != "seed" {
		t.Fatalf("README.md = %q, want the remote's", got)
	}
	trashed, err := filepath.Glob(filepath.Join(os.Getenv("XDG_DATA_HOME"), "Trash", "files", "README*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(trashed) != 1 || readFile(t, trashed[0]) != "local readme" {
		t.Fatalf("expected the local README.md edit in the trash, found %v", trashed)
	}
}
