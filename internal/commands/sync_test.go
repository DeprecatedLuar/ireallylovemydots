package commands

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/DeprecatedLuar/ireallylovemydots/internal/commands/shared"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/git"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/manifest"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/repo"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/state"
)

// markReadOnly records name as read-only in the machine access store — the
// state a repository this machine cannot push to is in by the time sync
// ever looks at it, per concept.md "Read-only repositories".
func markReadOnly(t *testing.T, name string) {
	t.Helper()
	access, err := state.ReadAccess()
	if err != nil {
		t.Fatal(err)
	}
	access.SetReadOnly(name, true)
	if err := state.WriteAccess(access); err != nil {
		t.Fatal(err)
	}
}

// installFakeGitPushAuthFailure prepends a directory to PATH holding a git
// wrapper that fails "git push" with an authentication-shaped stderr
// message and delegates every other invocation to the real git binary —
// the only reliable way to exercise a rejected push without a real remote
// that actually denies credentials.
func installFakeGitPushAuthFailure(t *testing.T) {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"push\" ]; then\n" +
		"  echo 'remote: Permission denied (publickey).' >&2\n" +
		"  echo 'fatal: Authentication failed for repository' >&2\n" +
		"  exit 1\n" +
		"fi\n" +
		"exec " + realGit + " \"$@\"\n"
	path := filepath.Join(dir, "git")
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

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
		if err := HandleSync(nil, shared.Flags{Repo: "repo-a"}); err != nil {
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
		if err := HandleSync(nil, shared.Flags{}); err != nil {
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
		if err := HandleSync(nil, shared.Flags{Repo: "big"}); err != nil {
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

// stageNamespaceRemoval deletes ns's folder and stages that deletion via
// git.StagePath — the state `rm` leaves a repository in (see
// rmNamespaceAt, internal/commands/rm.go) once the payload is trashed but
// before the next sync commits anything.
func stageNamespaceRemoval(t *testing.T, repoDir, ns string) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(repoDir, ns)); err != nil {
		t.Fatal(err)
	}
	if err := git.StagePath(repoDir, ns); err != nil {
		t.Fatalf("StagePath: %v", err)
	}
}

func TestHandleSync_RewindsRemovedNamespaceStillOnlyInUnpushedCommits(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	repoDir, remote := newRegisteredRepo(t, dataDir, scratchRoot, "repo", []string{"keep"})
	// Widens the sparse cone ahead of writing new namespaces below: unlike
	// `namespace add` (a plain filesystem move, unaffected by the cone),
	// writeSyncNamespace + `git add -A` needs the paths inside the cone to
	// be staged at all. repo.Add can't be used here — it verifies the path
	// materializes from the index afterward, which only holds once
	// something is actually tracked there.
	syncGitRun(t, repoDir, "sparse-checkout", "add", "extra", "secret")

	writeSyncNamespace(t, repoDir, "extra")
	syncGitRun(t, repoDir, "add", "-A")
	syncGitRun(t, repoDir, "commit", "-m", "add extra")

	writeSyncNamespace(t, repoDir, "secret")
	syncGitRun(t, repoDir, "add", "-A")
	syncGitRun(t, repoDir, "commit", "-m", "add secret")

	stageNamespaceRemoval(t, repoDir, "secret")

	// An unrelated uncommitted edit alongside the staged removal, so the
	// rewind still has something real to recommit on top of extraCommit —
	// exercising the same "sync still commits everything else" path a bare
	// deletion-only rewind (nothing left to commit) would skip.
	if err := os.WriteFile(filepath.Join(repoDir, "keep", "file"), []byte("changed keep"), 0644); err != nil {
		t.Fatal(err)
	}

	stdout, _ := captureStdoutStderr(t, func() {
		if err := HandleSync(nil, shared.Flags{Yes: true, Repo: "repo"}); err != nil {
			t.Fatalf("HandleSync: %v", err)
		}
	})
	if !strings.Contains(stdout, "repo") {
		t.Fatalf("expected the summary to report repo, got: %s", stdout)
	}

	log := strings.TrimSpace(syncGitRun(t, repoDir, "log", "--oneline", "HEAD", "--", "secret"))
	if log != "" {
		t.Fatalf("expected no commit reachable from HEAD to touch secret, got:\n%s", log)
	}
	if got := remoteFile(t, remote, "extra/file"); got != "extra" {
		t.Fatalf("expected the unrelated 'extra' namespace to reach the remote, got %q", got)
	}
	if content, err := os.ReadFile(filepath.Join(repoDir, "keep", "file")); err != nil || string(content) != "changed keep" {
		t.Fatalf("expected keep/file's edit to have been carried into the recommit, got %q, err=%v", content, err)
	}
	if content, err := os.ReadFile(filepath.Join(repoDir, "extra", "file")); err != nil || string(content) != "extra" {
		t.Fatalf("expected extra/file to be unchanged, got %q, err=%v", content, err)
	}

	localHead := strings.TrimSpace(syncGitRun(t, repoDir, "rev-parse", "HEAD"))
	remoteHead := strings.TrimSpace(syncGitRun(t, remote, "rev-parse", "main"))
	if localHead != remoteHead {
		t.Fatalf("expected repo to have been pushed, local=%s remote=%s", localHead, remoteHead)
	}
	remoteLog := strings.TrimSpace(syncGitRun(t, remote, "log", "--oneline", "main", "--", "secret"))
	if remoteLog != "" {
		t.Fatalf("expected the pushed history to contain nothing touching secret, got:\n%s", remoteLog)
	}
}

func TestHandleSync_RefusesRewindAndReportsWhenRemovedNamespaceAlreadyPushed(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	repoDir, remote := newRegisteredRepo(t, dataDir, scratchRoot, "repo", []string{"keep", "secret"})

	stageNamespaceRemoval(t, repoDir, "secret")

	stdout, stderr := captureStdoutStderr(t, func() {
		if err := HandleSync(nil, shared.Flags{Yes: true, Repo: "repo"}); err != nil {
			t.Fatalf("HandleSync: %v", err)
		}
	})
	if !strings.Contains(stdout+stderr, "already pushed") {
		t.Fatalf("expected a report that secret was already pushed, got stdout=%s stderr=%s", stdout, stderr)
	}

	localHead := strings.TrimSpace(syncGitRun(t, repoDir, "rev-parse", "HEAD"))
	remoteHead := strings.TrimSpace(syncGitRun(t, remote, "rev-parse", "main"))
	if localHead != remoteHead {
		t.Fatalf("expected repo to still sync and push its deletion commit, local=%s remote=%s", localHead, remoteHead)
	}
	deletions := strings.TrimSpace(syncGitRun(t, remote, "log", "--diff-filter=D", "--name-only", "main"))
	if !strings.Contains(deletions, "secret") {
		t.Fatalf("expected an ordinary deletion commit for secret since it could not be rewound, got:\n%s", deletions)
	}
}

func TestHandleSync_NonInteractiveWithoutYesLeavesRewindUndone(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	repoDir, remote := newRegisteredRepo(t, dataDir, scratchRoot, "repo", []string{"keep"})
	syncGitRun(t, repoDir, "sparse-checkout", "add", "secret")

	writeSyncNamespace(t, repoDir, "secret")
	syncGitRun(t, repoDir, "add", "-A")
	syncGitRun(t, repoDir, "commit", "-m", "add secret")
	headBefore := strings.TrimSpace(syncGitRun(t, repoDir, "rev-parse", "HEAD"))
	remoteHeadBefore := strings.TrimSpace(syncGitRun(t, remote, "rev-parse", "main"))

	stageNamespaceRemoval(t, repoDir, "secret")

	err := HandleSync(nil, shared.Flags{Repo: "repo"})
	if err == nil {
		t.Fatal("expected an error requiring -y in a non-interactive run")
	}

	headAfter := strings.TrimSpace(syncGitRun(t, repoDir, "rev-parse", "HEAD"))
	if headAfter != headBefore {
		t.Fatalf("expected HEAD to be untouched without confirmation, before=%s after=%s", headBefore, headAfter)
	}
	status := strings.TrimSpace(syncGitRun(t, repoDir, "status", "--porcelain"))
	if status == "" {
		t.Fatal("expected secret's staged removal to remain uncommitted")
	}
	remoteHead := strings.TrimSpace(syncGitRun(t, remote, "rev-parse", "main"))
	if remoteHead != remoteHeadBefore {
		t.Fatalf("expected nothing to have been pushed, remote=%s remoteHeadBefore=%s", remoteHead, remoteHeadBefore)
	}
}

// syncGitRunErr runs a git command without failing the test, for assertions
// that are themselves the pass/fail condition (e.g. merge-base
// --is-ancestor, which exits non-zero for "no").
func syncGitRunErr(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestHandleSync_ReadOnlyCleanRepoFastForwardsAndReportsSuccess(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	repoDir, remote := newRegisteredRepo(t, dataDir, scratchRoot, "repo", []string{"ns"})
	markReadOnly(t, "repo")

	peer := filepath.Join(scratchRoot, "peer")
	syncGitRun(t, scratchRoot, "clone", remote, peer)
	syncGitRun(t, peer, "config", "user.name", "peer")
	syncGitRun(t, peer, "config", "user.email", "peer@example.invalid")
	if err := os.WriteFile(filepath.Join(peer, "ns", "file"), []byte("from peer"), 0644); err != nil {
		t.Fatal(err)
	}
	syncGitRun(t, peer, "add", "-A")
	syncGitRun(t, peer, "commit", "-m", "peer edit")
	syncGitRun(t, peer, "push", "origin", "main")
	peerHead := strings.TrimSpace(syncGitRun(t, peer, "rev-parse", "HEAD"))

	var err error
	stdout, _ := captureStdoutStderr(t, func() {
		err = HandleSync(nil, shared.Flags{Repo: "repo"})
	})
	if err != nil {
		t.Fatalf("HandleSync: %v", err)
	}
	if !strings.Contains(stdout, "repo") {
		t.Fatalf("expected the summary to report repo, got: %s", stdout)
	}

	localHead := strings.TrimSpace(syncGitRun(t, repoDir, "rev-parse", "HEAD"))
	if localHead != peerHead {
		t.Fatalf("expected repo to fast-forward onto the peer's push and make no commit of its own, local=%s peer=%s", localHead, peerHead)
	}
	status := strings.TrimSpace(syncGitRun(t, repoDir, "status", "--porcelain"))
	if status != "" {
		t.Fatalf("expected a clean tree after fast-forward, got:\n%s", status)
	}
}

func TestHandleSync_PushAuthFailureSetsReadOnlyFlag(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	repoDir, _ := newRegisteredRepo(t, dataDir, scratchRoot, "repo", []string{"ns"})

	if err := os.WriteFile(filepath.Join(repoDir, "ns", "file"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}

	installFakeGitPushAuthFailure(t)

	var err error
	stdout, _ := captureStdoutStderr(t, func() {
		err = HandleSync(nil, shared.Flags{Repo: "repo"})
	})
	if !errors.Is(err, ErrSomeSkipped) {
		t.Fatalf("expected ErrSomeSkipped when the push is rejected, got %v", err)
	}
	if !strings.Contains(stdout, "repo") {
		t.Fatalf("expected the summary to report repo, got: %s", stdout)
	}

	access, err := state.ReadAccess()
	if err != nil {
		t.Fatal(err)
	}
	if !access.IsReadOnly("repo") {
		t.Fatal("expected the push authentication failure to flag repo read-only")
	}
}

// pushPeerEdit clones remote, writes rel, commits and pushes, standing in
// for another machine.
func pushPeerEdit(t *testing.T, scratchRoot, remote, rel, content string) {
	t.Helper()
	peer := filepath.Join(t.TempDir(), "peer")
	syncGitRun(t, scratchRoot, "clone", remote, peer)
	syncGitRun(t, peer, "config", "user.name", "peer")
	syncGitRun(t, peer, "config", "user.email", "peer@example.invalid")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(peer, rel)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(peer, rel), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	syncGitRun(t, peer, "add", "-A")
	syncGitRun(t, peer, "commit", "-m", "peer edit")
	syncGitRun(t, peer, "push", "origin", "main")
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func saveSyncMode(t *testing.T, repoName, ns, mode string) {
	t.Helper()
	s, err := state.Read()
	if err != nil {
		t.Fatal(err)
	}
	key := state.Key{Repo: repoName, Namespace: ns}
	e := s.Entries[key]
	e.SyncMode = mode
	s.Entries[key] = e
	if err := state.Write(s); err != nil {
		t.Fatal(err)
	}
}

func remoteFile(t *testing.T, remote, rel string) string {
	t.Helper()
	return syncGitRun(t, remote, "show", "main:"+rel)
}

func TestHandleSync_ConflictHoldsOnlyThatNamespaceAndRecordsItsBase(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	repoDir, remote := newRegisteredRepo(t, dataDir, scratchRoot, "repo", []string{"a", "b"})
	pushPeerEdit(t, scratchRoot, remote, "a/file", "peer a")
	writeTestFile(t, filepath.Join(repoDir, "a", "file"), "local a")
	writeTestFile(t, filepath.Join(repoDir, "b", "file"), "local b")

	var err error
	stdout, _ := captureStdoutStderr(t, func() { err = HandleSync(nil, shared.Flags{}) })
	if !errors.Is(err, ErrSomeSkipped) {
		t.Fatalf("err = %v, want ErrSomeSkipped for a held conflict", err)
	}
	for _, want := range []string{"held", "a: file", "dots sync a --overlay"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("summary missing %q:\n%s", want, stdout)
		}
	}
	if got := remoteFile(t, remote, "b/file"); got != "local b" {
		t.Fatalf("remote b/file = %q, want b pushed despite a's conflict", got)
	}
	if got := remoteFile(t, remote, "a/file"); got != "peer a" {
		t.Fatalf("remote a/file = %q, want the peer's version kept", got)
	}
	if got := readFile(t, filepath.Join(repoDir, "a", "file")); got != "local a" {
		t.Fatalf("held a/file = %q, want untouched", got)
	}
	s, _ := state.Read()
	if s.Entries[state.Key{Repo: "repo", Namespace: "a"}].HeldBase == "" {
		t.Fatal("expected a held base recorded for a")
	}

	// Next sync must not silently overwrite the peer's version.
	captureStdoutStderr(t, func() { err = HandleSync(nil, shared.Flags{}) })
	if !errors.Is(err, ErrSomeSkipped) {
		t.Fatalf("second sync err = %v, want the conflict still held", err)
	}
	if got := remoteFile(t, remote, "a/file"); got != "peer a" {
		t.Fatalf("remote a/file = %q after second sync; the held edit leaked", got)
	}
}

func TestHandleSync_OverwriteLocalTrashesEditsTakesRemoteAndClearsHeld(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	repoDir, remote := newRegisteredRepo(t, dataDir, scratchRoot, "repo", []string{"a"})
	pushPeerEdit(t, scratchRoot, remote, "a/file", "peer a")
	writeTestFile(t, filepath.Join(repoDir, "a", "file"), "local a")
	captureStdoutStderr(t, func() { _ = HandleSync(nil, shared.Flags{}) })

	var err error
	captureStdoutStderr(t, func() { err = HandleSync([]string{"a"}, shared.Flags{SyncMode: "overwrite-local"}) })
	if err != nil {
		t.Fatalf("HandleSync: %v", err)
	}
	if got := readFile(t, filepath.Join(repoDir, "a", "file")); got != "peer a" {
		t.Fatalf("a/file = %q, want the remote's", got)
	}
	if status := strings.TrimSpace(syncGitRun(t, repoDir, "status", "--porcelain")); status != "" {
		t.Fatalf("expected a clean tree, got:\n%s", status)
	}
	s, _ := state.Read()
	if s.Entries[state.Key{Repo: "repo", Namespace: "a"}].HeldBase != "" {
		t.Fatal("held base not cleared")
	}
}

func TestHandleSync_OverwriteRemotePushesLocalVersion(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	repoDir, remote := newRegisteredRepo(t, dataDir, scratchRoot, "repo", []string{"a"})
	pushPeerEdit(t, scratchRoot, remote, "a/file", "peer a")
	writeTestFile(t, filepath.Join(repoDir, "a", "file"), "local a")

	var err error
	captureStdoutStderr(t, func() { err = HandleSync([]string{"repo/a"}, shared.Flags{SyncMode: "overwrite-remote"}) })
	if err != nil {
		t.Fatalf("HandleSync: %v", err)
	}
	if got := remoteFile(t, remote, "a/file"); got != "local a" {
		t.Fatalf("remote a/file = %q, want local", got)
	}
}

func TestHandleSync_OverlayKeepsEditsAndUntrackedFilesUnpushed(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	repoDir, remote := newRegisteredRepo(t, dataDir, scratchRoot, "repo", []string{"a", "b"})
	saveSyncMode(t, "repo", "a", "overlay")
	pushPeerEdit(t, scratchRoot, remote, "a/other", "peer other")
	writeTestFile(t, filepath.Join(repoDir, "a", "file"), "local a")
	writeTestFile(t, filepath.Join(repoDir, "a", ".uuid"), "junk")
	writeTestFile(t, filepath.Join(repoDir, "b", "file"), "local b")

	var err error
	captureStdoutStderr(t, func() { err = HandleSync(nil, shared.Flags{}) })
	if err != nil {
		t.Fatalf("HandleSync: %v", err)
	}
	if got := remoteFile(t, remote, "a/file"); got != "a" {
		t.Fatalf("remote a/file = %q, overlay must not push", got)
	}
	if strings.Contains(syncGitRun(t, remote, "ls-tree", "-r", "--name-only", "main"), "a/.uuid") {
		t.Fatal("overlay pushed an untracked file")
	}
	if got := remoteFile(t, remote, "b/file"); got != "local b" {
		t.Fatalf("remote b/file = %q, want b merged and pushed", got)
	}
	if readFile(t, filepath.Join(repoDir, "a", "file")) != "local a" || readFile(t, filepath.Join(repoDir, "a", "other")) != "peer other" {
		t.Fatal("overlay namespace must hold local edits on top of the remote")
	}
}

func TestHandleSync_ScopedRunHoldsUnnamedEditsAndUpdatesUnnamedClean(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	repoDir, remote := newRegisteredRepo(t, dataDir, scratchRoot, "repo", []string{"a", "b", "c"})
	pushPeerEdit(t, scratchRoot, remote, "c/file", "peer c")
	writeTestFile(t, filepath.Join(repoDir, "a", "file"), "local a")
	writeTestFile(t, filepath.Join(repoDir, "b", "file"), "local b")

	var err error
	captureStdoutStderr(t, func() { err = HandleSync([]string{"a"}, shared.Flags{}) })
	if err != nil {
		t.Fatalf("HandleSync: %v", err)
	}
	if got := remoteFile(t, remote, "a/file"); got != "local a" {
		t.Fatalf("named a not pushed: %q", got)
	}
	if got := remoteFile(t, remote, "b/file"); got != "b" {
		t.Fatalf("unnamed b pushed: %q", got)
	}
	if got := readFile(t, filepath.Join(repoDir, "b", "file")); got != "local b" {
		t.Fatalf("unnamed b touched: %q", got)
	}
	if got := readFile(t, filepath.Join(repoDir, "c", "file")); got != "peer c" {
		t.Fatalf("unnamed clean c not updated: %q", got)
	}
}

func TestHandleSync_ReadOnlyWithUnpushedCommitsOverlaysOntoRemote(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	repoDir, remote := newRegisteredRepo(t, dataDir, scratchRoot, "repo", []string{"a", "b"})
	markReadOnly(t, "repo")
	pushPeerEdit(t, scratchRoot, remote, "b/file", "peer b")
	writeTestFile(t, filepath.Join(repoDir, "a", "file"), "local a")
	syncGitRun(t, repoDir, "add", "-A")
	syncGitRun(t, repoDir, "commit", "-m", "earlier local commit")
	remoteHeadBefore := strings.TrimSpace(syncGitRun(t, remote, "rev-parse", "main"))

	var err error
	captureStdoutStderr(t, func() { err = HandleSync(nil, shared.Flags{}) })
	if err != nil {
		t.Fatalf("HandleSync: %v", err)
	}
	if head := strings.TrimSpace(syncGitRun(t, repoDir, "rev-parse", "HEAD")); head != remoteHeadBefore {
		t.Fatalf("HEAD = %s, want the remote %s", head, remoteHeadBefore)
	}
	if strings.TrimSpace(syncGitRun(t, remote, "rev-parse", "main")) != remoteHeadBefore {
		t.Fatal("read-only repository pushed")
	}
	if readFile(t, filepath.Join(repoDir, "a", "file")) != "local a" || readFile(t, filepath.Join(repoDir, "b", "file")) != "peer b" {
		t.Fatal("expected local edits on top of the remote")
	}
}

func TestHandleSync_ReadOnlyOverwriteRemoteErrorsBeforeTouchingAnything(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	newRegisteredRepo(t, dataDir, scratchRoot, "repo", []string{"a"})
	markReadOnly(t, "repo")
	err := HandleSync([]string{"a"}, shared.Flags{SyncMode: "overwrite-remote"})
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("err = %v, want a read-only refusal", err)
	}
}

func TestHandleSync_ModeFlagWithoutTargetErrors(t *testing.T) {
	setupSyncEnv(t)
	if err := HandleSync(nil, shared.Flags{SyncMode: "overwrite-local"}); err == nil {
		t.Fatal("expected a bare mode flag to be refused")
	}
}

func TestHandleSync_RepoNameAsArgumentPointsAtRepoFlag(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	newRegisteredRepo(t, dataDir, scratchRoot, "repo", []string{"a"})
	err := HandleSync([]string{"repo"}, shared.Flags{})
	if err == nil || !strings.Contains(err.Error(), "--repo") {
		t.Fatalf("err = %v, want a hint naming --repo", err)
	}
}

func TestHandleSync_RootFileConflictFailsRepoAndTouchesNothing(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	repoDir, remote := newRegisteredRepo(t, dataDir, scratchRoot, "repo", []string{"a"})
	pushPeerEdit(t, scratchRoot, remote, "README", "peer")
	syncGitRun(t, repoDir, "fetch", "origin")
	syncGitRun(t, repoDir, "merge", "--ff-only", "origin/main")
	pushPeerEdit(t, scratchRoot, remote, "README", "peer again")
	writeTestFile(t, filepath.Join(repoDir, "README"), "local")
	headBefore := strings.TrimSpace(syncGitRun(t, repoDir, "rev-parse", "HEAD"))

	var err error
	stdout, _ := captureStdoutStderr(t, func() { err = HandleSync(nil, shared.Flags{}) })
	if !errors.Is(err, ErrSomeSkipped) || !strings.Contains(stdout, "README") {
		t.Fatalf("err = %v, stdout:\n%s\nwant a failed repo naming README", err, stdout)
	}
	if strings.TrimSpace(syncGitRun(t, repoDir, "rev-parse", "HEAD")) != headBefore || readFile(t, filepath.Join(repoDir, "README")) != "local" {
		t.Fatal("repository was touched")
	}
}

// pushPeerRemoval removes the named namespaces in a fresh peer clone and
// pushes, as namespace rm plus sync would on another machine.
func pushPeerRemoval(t *testing.T, scratchRoot, remote string, namespaces ...string) {
	t.Helper()
	peer := filepath.Join(t.TempDir(), "peer")
	syncGitRun(t, scratchRoot, "clone", remote, peer)
	syncGitRun(t, peer, "config", "user.name", "peer")
	syncGitRun(t, peer, "config", "user.email", "peer@example.invalid")
	syncGitRun(t, peer, append([]string{"rm", "-r", "-q"}, namespaces...)...)
	syncGitRun(t, peer, "commit", "-m", "peer removal")
	syncGitRun(t, peer, "push", "origin", "main")
}

// linkNamespace records ns as enabled with one live symlink at dest pointing
// into its folder, the state enable leaves behind.
func linkNamespace(t *testing.T, repoDir, repoName, ns string) (dest string) {
	t.Helper()
	dest = filepath.Join(t.TempDir(), ns)
	if err := os.Symlink(filepath.Join(repoDir, ns, "file"), dest); err != nil {
		t.Fatal(err)
	}
	s, err := state.Read()
	if err != nil {
		t.Fatal(err)
	}
	s.Entries[state.Key{Repo: repoName, Namespace: ns}] = state.Entry{Enabled: true, LinkedDests: []string{dest}}
	if err := state.Write(s); err != nil {
		t.Fatal(err)
	}
	return dest
}

func assertRemovedHere(t *testing.T, repoDir, repoName, ns, dest string) {
	t.Helper()
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatalf("link %s still present (err=%v)", dest, err)
	}
	if _, err := os.Lstat(filepath.Join(repoDir, ns)); !os.IsNotExist(err) {
		t.Fatalf("folder %s still present (err=%v); a leftover reads as an installed namespace", ns, err)
	}
	s, _ := state.Read()
	if _, ok := s.Entries[state.Key{Repo: repoName, Namespace: ns}]; ok {
		t.Fatalf("state entry for %s still present", ns)
	}
}

func TestHandleSync_UnchangedNamespaceDeletedUpstreamIsRemovedHere(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	repoDir, remote := newRegisteredRepo(t, dataDir, scratchRoot, "repo", []string{"a", "b"})
	dest := linkNamespace(t, repoDir, "repo", "a")
	pushPeerRemoval(t, scratchRoot, remote, "a")

	var err error
	stdout, _ := captureStdoutStderr(t, func() { err = HandleSync(nil, shared.Flags{}) })
	if err != nil {
		t.Fatalf("HandleSync: %v", err)
	}
	if !strings.Contains(stdout, "removed upstream: a") {
		t.Fatalf("summary missing removal:\n%s", stdout)
	}
	assertRemovedHere(t, repoDir, "repo", "a", dest)
}

func TestHandleSync_EditedNamespaceDeletedUpstreamIsTrashedNotResurrected(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	repoDir, remote := newRegisteredRepo(t, dataDir, scratchRoot, "repo", []string{"a", "b"})
	dest := linkNamespace(t, repoDir, "repo", "a")
	writeTestFile(t, filepath.Join(repoDir, "a", "file"), "local edit")
	pushPeerRemoval(t, scratchRoot, remote, "a")

	var err error
	captureStdoutStderr(t, func() { err = HandleSync(nil, shared.Flags{}) })
	if err != nil {
		t.Fatalf("HandleSync: %v", err)
	}
	assertRemovedHere(t, repoDir, "repo", "a", dest)

	trashed, _ := filepath.Glob(filepath.Join(os.Getenv("XDG_DATA_HOME"), "Trash", "files", "a*", "file"))
	if len(trashed) != 1 || readFile(t, trashed[0]) != "local edit" {
		t.Fatalf("trashed = %v, want the local edit recoverable from the trash", trashed)
	}

	captureStdoutStderr(t, func() { err = HandleSync(nil, shared.Flags{}) })
	if err != nil {
		t.Fatalf("second HandleSync: %v", err)
	}
	if out := syncGitRun(t, remote, "ls-tree", "--name-only", "main"); strings.Contains(out, "a\n") {
		t.Fatalf("remote tree regained a:\n%s", out)
	}
}

func TestHandleSync_OutOfScopeEditedRemovalStaysHeld(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	repoDir, remote := newRegisteredRepo(t, dataDir, scratchRoot, "repo", []string{"a", "b"})
	linkNamespace(t, repoDir, "repo", "a")
	writeTestFile(t, filepath.Join(repoDir, "a", "file"), "local edit")
	pushPeerRemoval(t, scratchRoot, remote, "a")

	captureStdoutStderr(t, func() { _ = HandleSync([]string{"b"}, shared.Flags{}) })
	if got := readFile(t, filepath.Join(repoDir, "a", "file")); got != "local edit" {
		t.Fatalf("a/file = %q, want the edit untouched by a sync scoped to b", got)
	}
}

func TestHandleSync_ManyUpstreamRemovalsNeedConfirmation(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	names := []string{"a", "b", "c", "d", "keep"}
	repoDir, remote := newRegisteredRepo(t, dataDir, scratchRoot, "repo", names)
	for _, n := range names {
		linkNamespace(t, repoDir, "repo", n)
	}
	pushPeerRemoval(t, scratchRoot, remote, "a", "b", "c", "d")

	var err error
	captureStdoutStderr(t, func() { err = HandleSync(nil, shared.Flags{}) })
	if err == nil {
		t.Fatal("expected a refusal: 4 removals, no terminal, no -y")
	}
	if _, statErr := os.Stat(filepath.Join(repoDir, "a")); statErr != nil {
		t.Fatalf("a was touched despite the refusal: %v", statErr)
	}

	captureStdoutStderr(t, func() { err = HandleSync(nil, shared.Flags{Yes: true}) })
	if err != nil {
		t.Fatalf("HandleSync -y: %v", err)
	}
	for _, n := range []string{"a", "b", "c", "d"} {
		if _, statErr := os.Lstat(filepath.Join(repoDir, n)); !os.IsNotExist(statErr) {
			t.Fatalf("%s still present after -y", n)
		}
	}
}
