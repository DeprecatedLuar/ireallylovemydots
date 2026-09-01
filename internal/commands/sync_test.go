package commands

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/repo"
)

// setupSyncEnv points the three XDG directories dots uses at fresh temp
// dirs and returns the data directory (where repository clones live) and
// a scratch root outside it for building bare remotes and peer clones.
func setupSyncEnv(t *testing.T) (dataDir, scratchRoot string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	dataDir = filepath.Join(dataHome, "ireallylovemydots")
	scratchRoot = t.TempDir()
	return dataDir, scratchRoot
}

func syncGitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// newBareRemote creates a bare repository under scratchRoot/name.git.
func newBareRemote(t *testing.T, scratchRoot, name string) string {
	t.Helper()
	remote := filepath.Join(scratchRoot, name+".git")
	syncGitRun(t, scratchRoot, "init", "--bare", "-b", "main", remote)
	return remote
}

// newRegisteredRepo builds a git repository at dataDir/name, remoted at a
// fresh bare repository under scratchRoot, seeds it with one namespace per
// entry in namespaces (each holding a manifest.Write'd .dots file and one
// tracked file), commits and pushes it, then registers it in the shared
// repository manifest — the on-disk shape `repo add` leaves behind.
func newRegisteredRepo(t *testing.T, dataDir, scratchRoot, name string, namespaces []string) (repoDir, remote string) {
	t.Helper()
	remote = newBareRemote(t, scratchRoot, name)

	repoDir = filepath.Join(dataDir, name)
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}
	syncGitRun(t, repoDir, "init", "-b", "main")
	syncGitRun(t, repoDir, "config", "user.name", "dots test")
	syncGitRun(t, repoDir, "config", "user.email", "dots@example.invalid")
	syncGitRun(t, repoDir, "remote", "add", "origin", remote)

	for _, ns := range namespaces {
		writeSyncNamespace(t, repoDir, ns)
	}
	syncGitRun(t, repoDir, "add", "-A")
	syncGitRun(t, repoDir, "commit", "-m", "seed")
	syncGitRun(t, repoDir, "push", "-u", "origin", "main")

	// Every registered clone is sparse by the time sync ever runs against
	// it — repo.Clone always creates one with --sparse, and repo init
	// converts a plain folder via EnsureSparse — so the fixture matches
	// that rather than leaving a plain, non-sparse checkout behind.
	if err := repo.EnsureSparse(repoDir, namespaces); err != nil {
		t.Fatal(err)
	}

	reg, err := manifest.ReadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	reg.Repos = append(reg.Repos, manifest.Repo{Name: name, Owner: "someone", URL: remote})
	if err := manifest.WriteRegistry(reg); err != nil {
		t.Fatal(err)
	}
	return repoDir, remote
}

func writeSyncNamespace(t *testing.T, repoDir, ns string) {
	t.Helper()
	nsDir := filepath.Join(repoDir, ns)
	if err := os.MkdirAll(nsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nsDir, "file"), []byte(ns), 0644); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Write(nsDir, manifest.Manifest{
		Entries: []manifest.Entry{{Name: "file", Dest: filepath.Join("~", ns, "file")}},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestHandleSync_NamedRepoOnlySyncsThatRepoAndLeavesOthersUntouched(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)

	repoA, remoteA := newRegisteredRepo(t, dataDir, scratchRoot, "repo-a", []string{"ns"})
	repoB, remoteB := newRegisteredRepo(t, dataDir, scratchRoot, "repo-b", []string{"ns"})

	if err := os.WriteFile(filepath.Join(repoA, "ns", "file"), []byte("changed in A"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoB, "ns", "file"), []byte("changed in B"), 0644); err != nil {
		t.Fatal(err)
	}

	stdout, _ := captureStdoutStderr(t, func() {
		if err := HandleSync([]string{"repo-a"}); err != nil {
			t.Fatalf("HandleSync: %v", err)
		}
	})
	if !strings.Contains(stdout, "repo-a") {
		t.Fatalf("expected the summary to name repo-a, got: %s", stdout)
	}
	if strings.Contains(stdout, "repo-b") {
		t.Fatalf("expected repo-b to be left untouched, got: %s", stdout)
	}

	statusA := strings.TrimSpace(syncGitRun(t, repoA, "status", "--porcelain"))
	if statusA != "" {
		t.Fatalf("expected repo-a to be committed and clean, got:\n%s", statusA)
	}
	remoteAHead := strings.TrimSpace(syncGitRun(t, remoteA, "rev-parse", "main"))
	localAHead := strings.TrimSpace(syncGitRun(t, repoA, "rev-parse", "HEAD"))
	if remoteAHead != localAHead {
		t.Fatalf("expected repo-a to be pushed: remote=%s local=%s", remoteAHead, localAHead)
	}

	statusB := strings.TrimSpace(syncGitRun(t, repoB, "status", "--porcelain"))
	if statusB == "" {
		t.Fatal("expected repo-b's uncommitted change to remain untouched")
	}
	remoteBHead := strings.TrimSpace(syncGitRun(t, remoteB, "rev-parse", "main"))
	localBHeadBeforeSync := strings.TrimSpace(syncGitRun(t, repoB, "rev-parse", "HEAD"))
	if remoteBHead != localBHeadBeforeSync {
		t.Fatal("expected repo-b to not have been pushed")
	}
}

func TestHandleSync_DivergingRepoStopsUncommittedAndExitsNonZeroWhileOthersSucceed(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)

	var repoDirs []string
	var remotes []string
	for i := 0; i < 5; i++ {
		name := "repo-" + strconv.Itoa(i)
		repoDir, remote := newRegisteredRepo(t, dataDir, scratchRoot, name, []string{"ns"})
		repoDirs = append(repoDirs, repoDir)
		remotes = append(remotes, remote)
	}

	// repo-2 (the third) diverges: a peer clone changes the same file and
	// pushes, while the registered clone changes it differently and is
	// left uncommitted for HandleSync to commit itself.
	divergeIdx := 2
	peer := filepath.Join(scratchRoot, "peer")
	syncGitRun(t, scratchRoot, "clone", remotes[divergeIdx], peer)
	syncGitRun(t, peer, "config", "user.name", "peer")
	syncGitRun(t, peer, "config", "user.email", "peer@example.invalid")
	if err := os.WriteFile(filepath.Join(peer, "ns", "file"), []byte("from peer"), 0644); err != nil {
		t.Fatal(err)
	}
	syncGitRun(t, peer, "add", "-A")
	syncGitRun(t, peer, "commit", "-m", "peer edit")
	syncGitRun(t, peer, "push", "origin", "main")

	if err := os.WriteFile(filepath.Join(repoDirs[divergeIdx], "ns", "file"), []byte("from local"), 0644); err != nil {
		t.Fatal(err)
	}

	// The other four also get a local edit, to prove they still sync and
	// push despite repo-2's failure.
	for i, dir := range repoDirs {
		if i == divergeIdx {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, "ns", "file"), []byte("edit "+strconv.Itoa(i)), 0644); err != nil {
			t.Fatal(err)
		}
	}

	var err error
	stdout, _ := captureStdoutStderr(t, func() {
		err = HandleSync(nil)
	})
	if !errors.Is(err, ErrSomeSkipped) {
		t.Fatalf("expected ErrSomeSkipped for a run with one diverging repository, got %v", err)
	}
	for i := 0; i < 5; i++ {
		name := "repo-" + strconv.Itoa(i)
		if !strings.Contains(stdout, name) {
			t.Fatalf("expected the summary to report %s, got: %s", name, stdout)
		}
	}

	// repo-2 stopped: not pushed, working tree clean, no rebase left in
	// progress, no conflict markers.
	statusDiverged := strings.TrimSpace(syncGitRun(t, repoDirs[divergeIdx], "status", "--porcelain"))
	if statusDiverged != "" {
		t.Fatalf("expected repo-2's tree to be clean after the stopped rebase, got:\n%s", statusDiverged)
	}
	if _, err := os.Stat(filepath.Join(repoDirs[divergeIdx], ".git", "rebase-merge")); err == nil {
		t.Fatal("expected no rebase left in progress on repo-2")
	}
	content, err := os.ReadFile(filepath.Join(repoDirs[divergeIdx], "ns", "file"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "<<<<<<<") {
		t.Fatalf("expected no conflict markers, got:\n%s", content)
	}
	remoteDivergedHead := strings.TrimSpace(syncGitRun(t, remotes[divergeIdx], "rev-parse", "main"))
	peerHead := strings.TrimSpace(syncGitRun(t, peer, "rev-parse", "HEAD"))
	if remoteDivergedHead != peerHead {
		t.Fatalf("expected repo-2 to not have been pushed: remote=%s peer=%s", remoteDivergedHead, peerHead)
	}

	// The other four synced and pushed cleanly.
	for i, dir := range repoDirs {
		if i == divergeIdx {
			continue
		}
		status := strings.TrimSpace(syncGitRun(t, dir, "status", "--porcelain"))
		if status != "" {
			t.Fatalf("repo-%d: expected a clean tree, got:\n%s", i, status)
		}
		localHead := strings.TrimSpace(syncGitRun(t, dir, "rev-parse", "HEAD"))
		remoteHead := strings.TrimSpace(syncGitRun(t, remotes[i], "rev-parse", "main"))
		if localHead != remoteHead {
			t.Fatalf("repo-%d: expected to have been pushed, local=%s remote=%s", i, localHead, remoteHead)
		}
	}
}

func TestHandleSync_NoRemoteRepoCommitsLocallyAlongsideRepoThatFetches(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)

	repoWithRemote, remote := newRegisteredRepo(t, dataDir, scratchRoot, "with-remote", []string{"ns"})
	if err := os.WriteFile(filepath.Join(repoWithRemote, "ns", "file"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}

	localOnlyDir := filepath.Join(dataDir, "local-only")
	if err := os.MkdirAll(localOnlyDir, 0755); err != nil {
		t.Fatal(err)
	}
	syncGitRun(t, localOnlyDir, "init", "-b", "main")
	syncGitRun(t, localOnlyDir, "config", "user.name", "dots test")
	syncGitRun(t, localOnlyDir, "config", "user.email", "dots@example.invalid")
	writeSyncNamespace(t, localOnlyDir, "ns")
	syncGitRun(t, localOnlyDir, "add", "-A")
	syncGitRun(t, localOnlyDir, "commit", "-m", "seed")
	if err := repo.EnsureSparse(localOnlyDir, []string{"ns"}); err != nil {
		t.Fatal(err)
	}

	reg, err := manifest.ReadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	reg.Repos = append(reg.Repos, manifest.Repo{Name: "local-only", Origin: manifest.OriginLocal})
	if err := manifest.WriteRegistry(reg); err != nil {
		t.Fatal(err)
	}

	stdout, _ := captureStdoutStderr(t, func() {
		if err := HandleSync(nil); err != nil {
			t.Fatalf("HandleSync: %v", err)
		}
	})
	if !strings.Contains(stdout, "with-remote") || !strings.Contains(stdout, "local-only") {
		t.Fatalf("expected the summary to report both repositories, got: %s", stdout)
	}

	statusLocalOnly := strings.TrimSpace(syncGitRun(t, localOnlyDir, "status", "--porcelain"))
	if statusLocalOnly != "" {
		t.Fatalf("expected local-only to be committed and clean, got:\n%s", statusLocalOnly)
	}
	head := strings.TrimSpace(syncGitRun(t, localOnlyDir, "rev-parse", "HEAD"))
	if head == "" {
		t.Fatal("expected local-only to have a commit")
	}

	statusWithRemote := strings.TrimSpace(syncGitRun(t, repoWithRemote, "status", "--porcelain"))
	if statusWithRemote != "" {
		t.Fatalf("expected with-remote to be committed and clean, got:\n%s", statusWithRemote)
	}
	localHead := strings.TrimSpace(syncGitRun(t, repoWithRemote, "rev-parse", "HEAD"))
	remoteHead := strings.TrimSpace(syncGitRun(t, remote, "rev-parse", "main"))
	if localHead != remoteHead {
		t.Fatalf("expected with-remote to have been pushed, local=%s remote=%s", localHead, remoteHead)
	}
}

func TestHandleSync_SparseRepoRebasesCleanlyWithNoStagedDeletionsAndRemoteGainsNoDeletion(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)

	const total = 20
	const installed = 3
	var namespaces []string
	for i := 0; i < total; i++ {
		namespaces = append(namespaces, "ns"+strconv.Itoa(i))
	}
	repoDir, remote := newRegisteredRepo(t, dataDir, scratchRoot, "big", namespaces)

	cone := namespaces[:installed]
	if err := repo.EnsureSparse(repoDir, cone); err != nil {
		t.Fatalf("EnsureSparse: %v", err)
	}
	for _, ns := range namespaces[installed:] {
		if _, err := os.Stat(filepath.Join(repoDir, ns)); err == nil {
			t.Fatalf("expected %s to be outside the cone after EnsureSparse", ns)
		}
	}

	// A peer machine changes a different installed namespace and pushes,
	// so the registered clone must fetch and rebase.
	peer := filepath.Join(scratchRoot, "peer")
	syncGitRun(t, scratchRoot, "clone", remote, peer)
	syncGitRun(t, peer, "config", "user.name", "peer")
	syncGitRun(t, peer, "config", "user.email", "peer@example.invalid")
	if err := os.WriteFile(filepath.Join(peer, "ns1", "file"), []byte("from peer"), 0644); err != nil {
		t.Fatal(err)
	}
	syncGitRun(t, peer, "add", "-A")
	syncGitRun(t, peer, "commit", "-m", "peer edit")
	syncGitRun(t, peer, "push", "origin", "main")

	// The registered clone has its own local edit to a different
	// installed namespace, so a real local commit is rebased and pushed —
	// exercising `git add -A` under the cone.
	if err := os.WriteFile(filepath.Join(repoDir, "ns0", "file"), []byte("from local"), 0644); err != nil {
		t.Fatal(err)
	}

	stdout, _ := captureStdoutStderr(t, func() {
		if err := HandleSync([]string{"big"}); err != nil {
			t.Fatalf("HandleSync: %v", err)
		}
	})
	if !strings.Contains(stdout, "big") {
		t.Fatalf("expected the summary to report big, got: %s", stdout)
	}

	status := strings.TrimSpace(syncGitRun(t, repoDir, "status", "--porcelain"))
	if status != "" {
		t.Fatalf("expected a clean git status after the rebase, got:\n%s", status)
	}
	if strings.Contains(status, "D ") {
		t.Fatalf("expected no staged deletions, got:\n%s", status)
	}

	cur, err := repo.List(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cur) != installed {
		t.Fatalf("expected the sparse cone to still hold %d namespaces after sync, got %v", installed, cur)
	}
	for _, ns := range namespaces[installed:] {
		if _, err := os.Stat(filepath.Join(repoDir, ns)); err == nil {
			t.Fatalf("expected %s to remain outside the cone after sync", ns)
		}
	}

	localHead := strings.TrimSpace(syncGitRun(t, repoDir, "rev-parse", "HEAD"))
	remoteHead := strings.TrimSpace(syncGitRun(t, remote, "rev-parse", "main"))
	if localHead != remoteHead {
		t.Fatalf("expected big to have been pushed, local=%s remote=%s", localHead, remoteHead)
	}

	deletions := strings.TrimSpace(syncGitRun(t, remote, "log", "--diff-filter=D", "--name-only", "main"))
	if deletions != "" {
		t.Fatalf("expected the remote to gain no deletion commit, got:\n%s", deletions)
	}
}
