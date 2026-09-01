package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newReconcileClones builds a bare remote plus two clones of it in a fresh
// temp dir, seeded with one committed and pushed file, mirroring the
// fixture dredge's reconcile_test.go uses.
func newReconcileClones(t *testing.T) (remote, first, second string) {
	t.Helper()
	root := t.TempDir()
	remote = filepath.Join(root, "remote.git")
	first = filepath.Join(root, "first")
	second = filepath.Join(root, "second")

	gitRun(t, root, "init", "--bare", "-b", "main", remote)
	gitRun(t, root, "clone", remote, first)
	configureReconcileRepo(t, first)
	writeReconcileFile(t, first, "seed", "seed")
	gitRun(t, first, "add", "-A")
	gitRun(t, first, "commit", "-m", "seed")
	gitRun(t, first, "push", "-u", "origin", "main")

	gitRun(t, root, "clone", remote, second)
	configureReconcileRepo(t, second)
	return remote, first, second
}

func configureReconcileRepo(t *testing.T, dir string) {
	t.Helper()
	gitRun(t, dir, "config", "user.name", "dots test")
	gitRun(t, dir, "config", "user.email", "dots@example.invalid")
}

func writeReconcileFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	full := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitCmd(dir, args...)
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return out
}

func TestReconcile_NoRemoteCommitsLocallyAndReportsNoRemote(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	configureReconcileRepo(t, dir)
	writeReconcileFile(t, dir, "namespace-a/file", "content")

	hasRemote, err := Reconcile(dir)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if hasRemote {
		t.Fatal("expected hasRemote=false for a repository with no remote")
	}

	head := strings.TrimSpace(gitRun(t, dir, "rev-parse", "HEAD"))
	if head == "" {
		t.Fatal("expected a commit to exist after Reconcile")
	}
	status := strings.TrimSpace(gitRun(t, dir, "status", "--porcelain"))
	if status != "" {
		t.Fatalf("expected a clean tree after Reconcile, got:\n%s", status)
	}
}

func TestReconcile_DivergingDifferentFilesSyncsBothDirections(t *testing.T) {
	_, first, second := newReconcileClones(t)

	writeReconcileFile(t, first, "namespace-a/file", "from first")
	if _, err := Reconcile(first); err != nil {
		t.Fatalf("Reconcile(first): %v", err)
	}
	if err := Push(first); err != nil {
		t.Fatalf("Push(first): %v", err)
	}

	writeReconcileFile(t, second, "namespace-b/file", "from second")
	if _, err := Reconcile(second); err != nil {
		t.Fatalf("Reconcile(second): %v", err)
	}
	if err := Push(second); err != nil {
		t.Fatalf("Push(second): %v", err)
	}

	if _, err := Reconcile(first); err != nil {
		t.Fatalf("Reconcile(first) second pass: %v", err)
	}

	gotA, err := os.ReadFile(filepath.Join(first, "namespace-a", "file"))
	if err != nil || string(gotA) != "from first" {
		t.Fatalf("namespace-a/file = %q, %v", gotA, err)
	}
	gotB, err := os.ReadFile(filepath.Join(first, "namespace-b", "file"))
	if err != nil || string(gotB) != "from second" {
		t.Fatalf("namespace-b/file = %q, %v", gotB, err)
	}

	status := strings.TrimSpace(gitRun(t, first, "status", "--porcelain"))
	if status != "" {
		t.Fatalf("expected a clean tree, got:\n%s", status)
	}
	if isRebaseInProgress(first) {
		t.Fatal("expected no rebase left in progress")
	}
}

func TestReconcile_FastPathWhenLocalEqualsRemote(t *testing.T) {
	_, first, second := newReconcileClones(t)
	_ = second

	hasRemote, err := Reconcile(first)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !hasRemote {
		t.Fatal("expected hasRemote=true")
	}
}

func TestReconcile_SameFileConflictStopsBothStatesReachableAndNotPushed(t *testing.T) {
	remote, first, second := newReconcileClones(t)
	_ = remote

	writeReconcileFile(t, first, "namespace-a/same", "from first")
	if _, err := Reconcile(first); err != nil {
		t.Fatalf("Reconcile(first): %v", err)
	}
	localHead := strings.TrimSpace(gitRun(t, first, "rev-parse", "HEAD"))
	if err := Push(first); err != nil {
		t.Fatalf("Push(first): %v", err)
	}

	writeReconcileFile(t, second, "namespace-a/same", "from second")
	_, err := Reconcile(second)
	if err == nil {
		t.Fatal("expected Reconcile to report a conflict")
	}
	if !strings.Contains(err.Error(), "both states are recoverable") {
		t.Fatalf("expected a recoverable-conflict message, got: %v", err)
	}

	// Working tree left clean, no rebase in progress, no conflict markers
	// in any tracked file.
	status := strings.TrimSpace(gitRun(t, second, "status", "--porcelain"))
	if status != "" {
		t.Fatalf("expected a clean tree after a stopped rebase, got:\n%s", status)
	}
	if isRebaseInProgress(second) {
		t.Fatal("expected the rebase to have been aborted")
	}
	content, readErr := os.ReadFile(filepath.Join(second, "namespace-a", "same"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(content), "<<<<<<<") {
		t.Fatalf("expected no conflict markers, got:\n%s", content)
	}

	// Both states are reachable: local HEAD is still the offline commit,
	// and the remote-tracking branch still names the pushed commit.
	head := strings.TrimSpace(gitRun(t, second, "rev-parse", "HEAD"))
	if head == localHead {
		t.Fatalf("HEAD moved to the local recovery target %s; expected the offline commit to remain HEAD", localHead)
	}
	remoteRef := strings.TrimSpace(gitRun(t, second, "rev-parse", "refs/remotes/origin/main"))
	if remoteRef != localHead {
		t.Fatalf("refs/remotes/origin/main = %s, want %s (unchanged, not pushed to)", remoteRef, localHead)
	}
	recoveryRef := strings.TrimSpace(gitRun(t, second, "rev-parse", recoveryRefNamespace+"/"+head))
	if recoveryRef != head {
		t.Fatalf("recovery ref = %s, want %s", recoveryRef, head)
	}

	// Not pushed: the remote's own history has not moved.
	remoteHeadAfter := strings.TrimSpace(gitRun(t, filepath.Dir(first), "ls-remote", filepath.Join(filepath.Dir(first), "remote.git"), "refs/heads/main"))
	if !strings.HasPrefix(remoteHeadAfter, localHead) {
		t.Fatalf("remote moved despite the conflict: %s", remoteHeadAfter)
	}
}

func TestReconcile_InterruptedRebaseIsAbortedAndRecoveredOnNextRun(t *testing.T) {
	_, first, second := newReconcileClones(t)

	writeReconcileFile(t, first, "namespace-a/same", "from first")
	if _, err := Reconcile(first); err != nil {
		t.Fatalf("Reconcile(first): %v", err)
	}
	if err := Push(first); err != nil {
		t.Fatalf("Push(first): %v", err)
	}

	writeReconcileFile(t, second, "namespace-a/same", "from second")
	gitRun(t, second, "add", "-A")
	gitRun(t, second, "commit", "-m", "offline edit")
	gitRun(t, second, "fetch", "origin", "main")
	if _, err := gitCmd(second, "rebase", "origin/main"); err == nil {
		t.Fatal("fixture did not leave an interrupted conflicting rebase")
	}
	if !isRebaseInProgress(second) {
		t.Fatal("fixture has no interrupted rebase")
	}

	// The crash-left rebase is a genuine content conflict (both sides
	// changed namespace-a/same), so Reconcile must still report it rather
	// than silently pick a side — "recovered" means the stale rebase is
	// aborted first, leaving a clean tree with both states reachable,
	// not that the conflict resolves itself.
	_, err := Reconcile(second)
	if err == nil || !strings.Contains(err.Error(), "both states are recoverable") {
		t.Fatalf("expected the same recoverable-conflict report, got: %v", err)
	}
	if isRebaseInProgress(second) {
		t.Fatal("expected no rebase left in progress after recovery")
	}
	status := strings.TrimSpace(gitRun(t, second, "status", "--porcelain"))
	if status != "" {
		t.Fatalf("expected a clean tree after recovery, got:\n%s", status)
	}
}

func TestReconcile_AbortsStaleRebaseBeforeReconciling(t *testing.T) {
	_, first, second := newReconcileClones(t)

	writeReconcileFile(t, first, "namespace-a/same", "from first")
	if _, err := Reconcile(first); err != nil {
		t.Fatalf("Reconcile(first): %v", err)
	}
	if err := Push(first); err != nil {
		t.Fatalf("Push(first): %v", err)
	}

	writeReconcileFile(t, second, "namespace-a/same", "from second")
	gitRun(t, second, "add", "-A")
	gitRun(t, second, "commit", "-m", "offline edit")
	gitRun(t, second, "fetch", "origin", "main")
	if _, err := gitCmd(second, "rebase", "origin/main"); err == nil {
		t.Fatal("fixture did not leave an interrupted conflicting rebase")
	}

	writeReconcileFile(t, second, "namespace-c/new", "uncommitted change made after the crash")

	// The stale rebase recurs into the same genuine conflict, so Reconcile
	// reports it again — but the abort-then-commit ordering must still
	// have committed the unrelated uncommitted change first.
	if _, err := Reconcile(second); err == nil {
		t.Fatal("expected the same conflict to resurface after the stale rebase is aborted")
	}
	out := gitRun(t, second, "log", "--all", "--pretty=%H", "-1", "--", "namespace-c/new")
	if strings.TrimSpace(out) == "" {
		t.Fatal("expected namespace-c/new to have been committed before the rebase was retried")
	}
	got, err := os.ReadFile(filepath.Join(second, "namespace-c", "new"))
	if err != nil || string(got) != "uncommitted change made after the crash" {
		t.Fatalf("namespace-c/new = %q, %v", got, err)
	}
}

func TestReconcile_CommitMessageClassifiesNamespaces(t *testing.T) {
	_, first, _ := newReconcileClones(t)

	writeReconcileFile(t, first, "namespace-added/file", "new")
	if _, err := Reconcile(first); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	msg := strings.TrimSpace(gitRun(t, first, "log", "-1", "--pretty=%s"))
	if !strings.Contains(msg, "add namespace-added") {
		t.Fatalf("commit message = %q, want it to classify namespace-added as added", msg)
	}

	if err := os.WriteFile(filepath.Join(first, "namespace-added", "file"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Reconcile(first); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	msg = strings.TrimSpace(gitRun(t, first, "log", "-1", "--pretty=%s"))
	if !strings.Contains(msg, "upd namespace-added") {
		t.Fatalf("commit message = %q, want it to classify namespace-added as updated", msg)
	}

	if err := os.Remove(filepath.Join(first, "namespace-added", "file")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(first, "namespace-added")); err != nil {
		t.Fatal(err)
	}
	if _, err := Reconcile(first); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	msg = strings.TrimSpace(gitRun(t, first, "log", "-1", "--pretty=%s"))
	if !strings.Contains(msg, "del namespace-added") {
		t.Fatalf("commit message = %q, want it to classify namespace-added as removed", msg)
	}
}
