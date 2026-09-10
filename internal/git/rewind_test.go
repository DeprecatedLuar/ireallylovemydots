package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newRewindClone builds a bare remote plus one clone of it, seeded with one
// committed and pushed namespace, mirroring newReconcileClones but as a
// single clone since these tests only ever advance one side.
func newRewindClone(t *testing.T, namespaces ...string) (remote, dir string) {
	t.Helper()
	root := t.TempDir()
	remote = filepath.Join(root, "remote.git")
	dir = filepath.Join(root, "clone")

	gitRun(t, root, "init", "--bare", "-b", "main", remote)
	gitRun(t, root, "clone", remote, dir)
	configureReconcileRepo(t, dir)
	for _, ns := range namespaces {
		writeReconcileFile(t, dir, ns+"/file", ns)
	}
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "seed")
	gitRun(t, dir, "push", "-u", "origin", "main")
	return remote, dir
}

// addNamespaceCommit writes namespace ns's file with content and commits it
// (without pushing), returning the new commit's hash.
func addNamespaceCommit(t *testing.T, dir, ns, content string) string {
	t.Helper()
	writeReconcileFile(t, dir, ns+"/file", content)
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "touch "+ns)
	return strings.TrimSpace(gitRun(t, dir, "rev-parse", "HEAD"))
}

func TestUnpushedCommitsTouching_OnlyReturnsCommitsAboveUpstreamTouchingPath(t *testing.T) {
	_, dir := newRewindClone(t, "keep")

	unrelated := addNamespaceCommit(t, dir, "extra", "extra")
	touching := addNamespaceCommit(t, dir, "secret", "v1")

	got, err := UnpushedCommitsTouching(dir, []string{"secret"})
	if err != nil {
		t.Fatalf("UnpushedCommitsTouching: %v", err)
	}
	if len(got) != 1 || got[0] != touching {
		t.Fatalf("expected only %s, got %v (unrelated commit %s must be excluded)", touching, got, unrelated)
	}
}

func TestUnpushedCommitsTouching_EmptyWhenPathOnlyPushed(t *testing.T) {
	_, dir := newRewindClone(t, "keep")

	got, err := UnpushedCommitsTouching(dir, []string{"keep"})
	if err != nil {
		t.Fatalf("UnpushedCommitsTouching: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no unpushed commits touching an already-pushed namespace, got %v", got)
	}
}

func TestPushedCommitsTouching_CountsOnlyCommitsReachableFromUpstream(t *testing.T) {
	_, dir := newRewindClone(t, "keep")
	addNamespaceCommit(t, dir, "secret", "v1")

	pushedKeep, err := PushedCommitsTouching(dir, []string{"keep"})
	if err != nil {
		t.Fatalf("PushedCommitsTouching(keep): %v", err)
	}
	if pushedKeep != 1 {
		t.Fatalf("expected keep to have 1 pushed commit, got %d", pushedKeep)
	}

	pushedSecret, err := PushedCommitsTouching(dir, []string{"secret"})
	if err != nil {
		t.Fatalf("PushedCommitsTouching(secret): %v", err)
	}
	if pushedSecret != 0 {
		t.Fatalf("expected secret to have 0 pushed commits (it was never pushed), got %d", pushedSecret)
	}
}

func TestRewindTarget_IsParentOfEarliestUnpushedCommitTouchingPath(t *testing.T) {
	_, dir := newRewindClone(t, "keep")

	afterExtra := addNamespaceCommit(t, dir, "extra", "extra")
	addNamespaceCommit(t, dir, "secret", "v1")

	target, err := RewindTarget(dir, []string{"secret"})
	if err != nil {
		t.Fatalf("RewindTarget: %v", err)
	}
	if target != afterExtra {
		t.Fatalf("expected target %s (the commit before secret was touched), got %s", afterExtra, target)
	}
}

func TestRewindTarget_ErrorsWhenNothingUnpushedTouchesPath(t *testing.T) {
	_, dir := newRewindClone(t, "keep")

	if _, err := RewindTarget(dir, []string{"keep"}); err == nil {
		t.Fatal("expected an error when no unpushed commit touches the path")
	}
}

func TestRewindAndRecommit_DropsPathAndKeepsOtherNamespacesIntact(t *testing.T) {
	remote, dir := newRewindClone(t, "keep")

	addNamespaceCommit(t, dir, "extra", "extra")
	addNamespaceCommit(t, dir, "secret", "v1")

	// Mirrors what `rm` leaves behind: the payload is gone from disk and its
	// deletion is staged, but nothing has been committed yet.
	if err := os.RemoveAll(filepath.Join(dir, "secret")); err != nil {
		t.Fatal(err)
	}
	if err := StagePath(dir, "secret"); err != nil {
		t.Fatalf("StagePath: %v", err)
	}

	target, err := RewindTarget(dir, []string{"secret"})
	if err != nil {
		t.Fatalf("RewindTarget: %v", err)
	}
	if err := RewindAndRecommit(dir, target); err != nil {
		t.Fatalf("RewindAndRecommit: %v", err)
	}

	log := gitRun(t, dir, "log", "--oneline", "HEAD", "--", "secret")
	if strings.TrimSpace(log) != "" {
		t.Fatalf("expected no commit reachable from HEAD to touch secret, got:\n%s", log)
	}
	if _, err := os.Stat(filepath.Join(dir, "keep", "file")); err != nil {
		t.Fatalf("expected keep/file to survive the rewind: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "extra", "file")); err != nil {
		t.Fatalf("expected extra/file to survive the rewind: %v", err)
	}
	if content, err := os.ReadFile(filepath.Join(dir, "extra", "file")); err != nil || string(content) != "extra" {
		t.Fatalf("expected extra/file's content to be unchanged, got %q, err=%v", content, err)
	}

	status := strings.TrimSpace(gitRun(t, dir, "status", "--porcelain"))
	if status != "" {
		t.Fatalf("expected a clean tree after RewindAndRecommit, got:\n%s", status)
	}

	gitRun(t, dir, "push", "origin", "main")
	remoteLog := gitRun(t, remote, "log", "--oneline", "main", "--", "secret")
	if strings.TrimSpace(remoteLog) != "" {
		t.Fatalf("expected the pushed history to contain nothing touching secret, got:\n%s", remoteLog)
	}
}
