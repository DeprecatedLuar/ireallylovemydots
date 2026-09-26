package git

import (
	"errors"
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

func TestReconcile_DivergingDifferentFilesSyncsBothDirections(t *testing.T) {
	_, first, second := newReconcileClones(t)

	writeReconcileFile(t, first, "namespace-a/file", "from first")
	if _, err := Reconcile(first, SideNone); err != nil {
		t.Fatalf("Reconcile(first, SideNone): %v", err)
	}
	if err := Push(first); err != nil {
		t.Fatalf("Push(first): %v", err)
	}

	writeReconcileFile(t, second, "namespace-b/file", "from second")
	if _, err := Reconcile(second, SideNone); err != nil {
		t.Fatalf("Reconcile(second, SideNone): %v", err)
	}
	if err := Push(second); err != nil {
		t.Fatalf("Push(second): %v", err)
	}

	if _, err := Reconcile(first, SideNone); err != nil {
		t.Fatalf("Reconcile(first, SideNone) second pass: %v", err)
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

	result, err := Reconcile(first, SideNone)
	hasRemote := result.RemoteConfigured
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

	// The file exists on both sides first: an add/add conflict has no base
	// stage and is not a content conflict a side flag can settle.
	writeReconcileFile(t, first, "namespace-a/same", "base")
	gitRun(t, first, "add", "-A")
	gitRun(t, first, "commit", "-m", "base")
	gitRun(t, first, "push", "origin", "main")
	gitRun(t, second, "pull", "--rebase", "origin", "main")

	writeReconcileFile(t, first, "namespace-a/same", "from first")
	if _, err := Reconcile(first, SideNone); err != nil {
		t.Fatalf("Reconcile(first, SideNone): %v", err)
	}
	localHead := strings.TrimSpace(gitRun(t, first, "rev-parse", "HEAD"))
	if err := Push(first); err != nil {
		t.Fatalf("Push(first): %v", err)
	}

	writeReconcileFile(t, second, "namespace-a/same", "from second")
	_, err := Reconcile(second, SideNone)
	if err == nil {
		t.Fatal("expected Reconcile to report a conflict")
	}
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected a *ConflictError, got: %v", err)
	}
	if !conflict.Resolvable || len(conflict.Paths) != 1 || conflict.Paths[0] != "namespace-a/same" {
		t.Fatalf("conflict = %+v, want resolvable with Paths [namespace-a/same]", conflict)
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
	if !isRebaseInProgress(second) {
		t.Fatal("fixture has no interrupted rebase")
	}

	// The crash-left rebase is a genuine content conflict (both sides
	// changed namespace-a/same), so Reconcile must still report it rather
	// than silently pick a side — "recovered" means the stale rebase is
	// aborted first, leaving a clean tree with both states reachable,
	// not that the conflict resolves itself.
	_, err := Reconcile(second, SideNone)
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected the same conflict report, got: %v", err)
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

// conflictSetup builds the shared fixture for side-flag tests: a file "a" of
// three lines and a file "b" seeded on both clones. first then changes a's
// line 1 and pushes; b is added on first; second's local commit changes a's
// line 1 differently. Returns second, whose local edit is committed by the
// Reconcile under test.
func conflictSetup(t *testing.T) (first, second string) {
	t.Helper()
	_, first, second = newReconcileClones(t)
	writeReconcileFile(t, first, "a", "one\ntwo\nthree\n")
	gitRun(t, first, "add", "-A")
	gitRun(t, first, "commit", "-m", "add a")
	gitRun(t, first, "push", "origin", "main")
	gitRun(t, second, "pull", "--rebase", "origin", "main")

	// remote: edits line 1 (conflicting) and line 3 (not), and adds b.
	writeReconcileFile(t, first, "a", "remote one\ntwo\nremote three\n")
	writeReconcileFile(t, first, "b", "remote b")
	gitRun(t, first, "add", "-A")
	gitRun(t, first, "commit", "-m", "remote edits")
	gitRun(t, first, "push", "origin", "main")

	writeReconcileFile(t, second, "a", "local one\ntwo\nthree\n")
	return first, second
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

func recoveryRefs(t *testing.T, dir string) string {
	t.Helper()
	return strings.TrimSpace(gitRun(t, dir, "for-each-ref", recoveryRefNamespace))
}

func TestReconcile_SideLocalKeepsLocalLineAndEverythingElseFromRemote(t *testing.T) {
	_, second := conflictSetup(t)

	result, err := Reconcile(second, SideLocal)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(result.Overridden) != 1 || result.Overridden[0] != "a" {
		t.Fatalf("Overridden = %v, want [a]", result.Overridden)
	}
	if got := readFileString(t, second, "a"); got != "local one\ntwo\nremote three\n" {
		t.Fatalf("a = %q, want local's line with remote's other line", got)
	}
	if got := readFileString(t, second, "b"); got != "remote b" {
		t.Fatalf("b = %q", got)
	}
	assertSettledCleanly(t, second)
	if refs := recoveryRefs(t, second); refs != "" {
		t.Fatalf("expected the recovery ref deleted, got %q", refs)
	}
}

func TestReconcile_SideRemoteKeepsRemoteLine(t *testing.T) {
	_, second := conflictSetup(t)

	result, err := Reconcile(second, SideRemote)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(result.Overridden) != 1 || result.Overridden[0] != "a" {
		t.Fatalf("Overridden = %v, want [a]", result.Overridden)
	}
	if got := readFileString(t, second, "a"); got != "remote one\ntwo\nremote three\n" {
		t.Fatalf("a = %q, want remote's lines", got)
	}
	if got := readFileString(t, second, "b"); got != "remote b" {
		t.Fatalf("b = %q", got)
	}
	assertSettledCleanly(t, second)
	if refs := recoveryRefs(t, second); refs != "" {
		t.Fatalf("expected the recovery ref deleted, got %q", refs)
	}
}

func TestReconcile_TwoLocalCommitsConflictingOnDifferentFilesListsBoth(t *testing.T) {
	_, first, second := newReconcileClones(t)
	writeReconcileFile(t, first, "a", "a base")
	writeReconcileFile(t, first, "b", "b base")
	gitRun(t, first, "add", "-A")
	gitRun(t, first, "commit", "-m", "base")
	gitRun(t, first, "push", "origin", "main")
	gitRun(t, second, "pull", "--rebase", "origin", "main")

	writeReconcileFile(t, first, "a", "a remote")
	writeReconcileFile(t, first, "b", "b remote")
	gitRun(t, first, "add", "-A")
	gitRun(t, first, "commit", "-m", "remote")
	gitRun(t, first, "push", "origin", "main")

	writeReconcileFile(t, second, "a", "a local")
	gitRun(t, second, "add", "-A")
	gitRun(t, second, "commit", "-m", "local a")
	writeReconcileFile(t, second, "b", "b local")
	gitRun(t, second, "add", "-A")
	gitRun(t, second, "commit", "-m", "local b")

	_, err := Reconcile(second, SideNone)
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected a *ConflictError, got %v", err)
	}
	if strings.Join(conflict.Paths, ",") != "a,b" {
		t.Fatalf("Paths = %v, want [a b]", conflict.Paths)
	}

	// Settling with a side must settle both.
	result, err := Reconcile(second, SideLocal)
	if err != nil {
		t.Fatalf("Reconcile(SideLocal): %v", err)
	}
	if strings.Join(result.Overridden, ",") != "a,b" {
		t.Fatalf("Overridden = %v, want [a b]", result.Overridden)
	}
	if readFileString(t, second, "a") != "a local" || readFileString(t, second, "b") != "b local" {
		t.Fatal("expected local content to win in both files")
	}
}

func TestReconcile_ModifyDeleteConflictIsNotResolvableBySide(t *testing.T) {
	_, first, second := newReconcileClones(t)
	writeReconcileFile(t, first, "a", "base")
	gitRun(t, first, "add", "-A")
	gitRun(t, first, "commit", "-m", "base")
	gitRun(t, first, "push", "origin", "main")
	gitRun(t, second, "pull", "--rebase", "origin", "main")

	gitRun(t, first, "rm", "a")
	gitRun(t, first, "commit", "-m", "remote deletes a")
	gitRun(t, first, "push", "origin", "main")

	writeReconcileFile(t, second, "a", "local edit")

	_, err := Reconcile(second, SideLocal)
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected a *ConflictError, got %v", err)
	}
	if conflict.Resolvable {
		t.Fatal("expected Resolvable=false for a modify/delete conflict")
	}
	if len(conflict.Paths) != 1 || conflict.Paths[0] != "a" {
		t.Fatalf("Paths = %v, want [a]", conflict.Paths)
	}
	assertSettledCleanly(t, second)
}

// addAddSetup makes both clones create "n" with different content. Returns
// second, whose local file is committed by the Reconcile under test.
func addAddSetup(t *testing.T) string {
	t.Helper()
	_, first, second := newReconcileClones(t)
	writeReconcileFile(t, first, "n", "remote new")
	gitRun(t, first, "add", "-A")
	gitRun(t, first, "commit", "-m", "remote adds n")
	gitRun(t, first, "push", "origin", "main")
	writeReconcileFile(t, second, "n", "local new")
	return second
}

func TestReconcile_AddAddConflictIsResolvableBySide(t *testing.T) {
	second := addAddSetup(t)

	_, err := Reconcile(second, SideNone)
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected a *ConflictError, got %v", err)
	}
	if !conflict.Resolvable {
		t.Fatal("expected Resolvable=true for an add/add conflict")
	}
	if len(conflict.Paths) != 1 || conflict.Paths[0] != "n" {
		t.Fatalf("Paths = %v, want [n]", conflict.Paths)
	}
	assertSettledCleanly(t, second)
}

func TestReconcile_AddAddSideLocalAndSideRemoteKeepChosenContent(t *testing.T) {
	cases := []struct {
		name string
		side Side
		want string
	}{
		{"local", SideLocal, "local new"},
		{"remote", SideRemote, "remote new"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			second := addAddSetup(t)
			result, err := Reconcile(second, tc.side)
			if err != nil {
				t.Fatalf("Reconcile: %v", err)
			}
			if len(result.Overridden) != 1 || result.Overridden[0] != "n" {
				t.Fatalf("Overridden = %v, want [n]", result.Overridden)
			}
			if got := readFileString(t, second, "n"); got != tc.want {
				t.Fatalf("n = %q, want %q", got, tc.want)
			}
			assertSettledCleanly(t, second)
		})
	}
}

func TestReconcile_SideWithoutConflictOverridesNothing(t *testing.T) {
	_, first, second := newReconcileClones(t)
	writeReconcileFile(t, first, "x", "from first")
	gitRun(t, first, "add", "-A")
	gitRun(t, first, "commit", "-m", "x")
	gitRun(t, first, "push", "origin", "main")
	writeReconcileFile(t, second, "y", "from second")

	result, err := Reconcile(second, SideLocal)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(result.Overridden) != 0 {
		t.Fatalf("Overridden = %v, want empty", result.Overridden)
	}
}
