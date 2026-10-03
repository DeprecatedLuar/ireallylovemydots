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

	result, err := Reconcile(dir, SideNone)
	hasRemote := result.RemoteConfigured
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

func TestReconcile_FastPathWhenLocalEqualsRemote(t *testing.T) {
	_, first, second := newReconcileClones(t)
	_ = second

	result, err := Reconcile(first, SideNone)
	hasRemote := result.RemoteConfigured
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !hasRemote {
		t.Fatal("expected hasRemote=true")
	}
}

func TestReconcile_AbortsStaleRebaseBeforeReconciling(t *testing.T) {
	_, first, second := newReconcileClones(t)

	writeReconcileFile(t, first, "namespace-a/same", "from first")
	if _, err := Reconcile(first, SideNone); err != nil {
		t.Fatalf("Reconcile(first, SideNone): %v", err)
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
	if _, err := Reconcile(second, SideNone); err == nil {
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
	if _, err := Reconcile(first, SideNone); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	msg := strings.TrimSpace(gitRun(t, first, "log", "-1", "--pretty=%s"))
	if !strings.Contains(msg, "add namespace-added") {
		t.Fatalf("commit message = %q, want it to classify namespace-added as added", msg)
	}

	if err := os.WriteFile(filepath.Join(first, "namespace-added", "file"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Reconcile(first, SideNone); err != nil {
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
	if _, err := Reconcile(first, SideNone); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	msg = strings.TrimSpace(gitRun(t, first, "log", "-1", "--pretty=%s"))
	if !strings.Contains(msg, "del namespace-added") {
		t.Fatalf("commit message = %q, want it to classify namespace-added as removed", msg)
	}
}

func assertSettledCleanly(t *testing.T, dir string) {
	t.Helper()
	if isRebaseInProgress(dir) {
		t.Fatal("expected no rebase in progress")
	}
	if status := strings.TrimSpace(gitRun(t, dir, "status", "--porcelain")); status != "" {
		t.Fatalf("expected a clean tree, got:\n%s", status)
	}
}

func readFileString(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestReconcile_OnlyRemoteMovedFastForwards(t *testing.T) {
	_, first, second := newReconcileClones(t)
	writeReconcileFile(t, first, "new", "x")
	gitRun(t, first, "add", "-A")
	gitRun(t, first, "commit", "-m", "remote")
	gitRun(t, first, "push", "origin", "main")
	remoteHead := strings.TrimSpace(gitRun(t, first, "rev-parse", "HEAD"))

	if _, err := Reconcile(second, SideNone); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if head := strings.TrimSpace(gitRun(t, second, "rev-parse", "HEAD")); head != remoteHead {
		t.Fatalf("HEAD = %s, want %s", head, remoteHead)
	}
	if got := readFileString(t, second, "new"); got != "x" {
		t.Fatalf("new = %q", got)
	}
}

func TestReconcile_OnlyLocalMovedLeavesHeadUnchanged(t *testing.T) {
	_, _, second := newReconcileClones(t)
	writeReconcileFile(t, second, "new", "x")
	gitRun(t, second, "add", "-A")
	gitRun(t, second, "commit", "-m", "local")
	localHead := strings.TrimSpace(gitRun(t, second, "rev-parse", "HEAD"))

	if _, err := Reconcile(second, SideNone); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if head := strings.TrimSpace(gitRun(t, second, "rev-parse", "HEAD")); head != localHead {
		t.Fatalf("HEAD = %s, want %s", head, localHead)
	}
}
