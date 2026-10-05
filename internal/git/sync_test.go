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

// placeAll gives every unit of p the same placement.
func placeAll(p Prepared, pl Placement) map[string]Placement {
	m := map[string]Placement{}
	for _, u := range p.Units {
		m[u.Name] = pl
	}
	return m
}

func unitNamed(t *testing.T, p Prepared, name string) Unit {
	t.Helper()
	for _, u := range p.Units {
		if u.Name == name {
			return u
		}
	}
	t.Fatalf("no unit %q in %+v", name, p.Units)
	return Unit{}
}

func pushEdit(t *testing.T, dir, rel, content string) {
	t.Helper()
	gitRun(t, dir, "pull", "origin", "main")
	writeReconcileFile(t, dir, rel, content)
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "edit "+rel)
	gitRun(t, dir, "push", "origin", "main")
}

var merged = Placement{Commit: SourceMerged, Worktree: SourceMerged}

func TestPrepareFinish_MergeBothSidesAndPushable(t *testing.T) {
	_, first, second := newReconcileClones(t)
	pushEdit(t, first, "a/file", "remote")
	writeReconcileFile(t, second, "b/file", "local")

	p, err := Prepare(second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if u := unitNamed(t, p, "b"); !u.ChangedLocal || !u.Dir {
		t.Fatalf("b = %+v, want a locally changed namespace", u)
	}
	if u := unitNamed(t, p, "a"); !u.ChangedRemote || u.ChangedLocal {
		t.Fatalf("a = %+v, want changed remotely only", u)
	}
	pushable, err := Finish(second, p, placeAll(p, merged), true)
	if err != nil {
		t.Fatal(err)
	}
	if !pushable {
		t.Fatal("expected a commit to push")
	}
	if readFileString(t, second, "a/file") != "remote" || readFileString(t, second, "b/file") != "local" {
		t.Fatal("expected both sides on disk")
	}
	if parent := strings.TrimSpace(gitRun(t, second, "rev-parse", "HEAD~1")); parent != p.Remote {
		t.Fatalf("HEAD~1 = %s, want remote %s", parent, p.Remote)
	}
	assertSettledCleanly(t, second)
}

// A blobless clone fetches a changed root file's tree entry but not its
// blob; sync must still build trees from it.
func TestPrepareFinish_BloblessCloneRemoteRootFileChange(t *testing.T) {
	remote, first, _ := newReconcileClones(t)
	gitRun(t, remote, "config", "uploadpack.allowFilter", "true")
	blobless := filepath.Join(t.TempDir(), "blobless")
	gitRun(t, "", "clone", "--filter=blob:none", "file://"+remote, blobless)
	configureReconcileRepo(t, blobless)
	pushEdit(t, first, "seed", "remote")

	p, err := Prepare(blobless, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Finish(blobless, p, placeAll(p, merged), true); err != nil {
		t.Fatal(err)
	}
	if got := readFileString(t, blobless, "seed"); got != "remote" {
		t.Fatalf("seed = %q, want remote", got)
	}
	assertSettledCleanly(t, blobless)
}

func TestPrepareFinish_OverlayKeepsEditsOnTopUncommitted(t *testing.T) {
	_, first, second := newReconcileClones(t)
	writeReconcileFile(t, first, "ns/settings", "l1\nl2\nl3\nl4\nl5\n")
	gitRun(t, first, "add", "-A")
	gitRun(t, first, "commit", "-m", "ns")
	gitRun(t, first, "push", "origin", "main")
	gitRun(t, second, "pull", "origin", "main")
	pushEdit(t, first, "ns/settings", "l1\nl2\nl3\nl4\nl5-remote\n")
	writeReconcileFile(t, second, "ns/settings", "l1-local\nl2\nl3\nl4\nl5\n")
	writeReconcileFile(t, second, "ns/new", "junk")

	overlay := Placement{Commit: SourceRemote, Worktree: SourceMerged}
	for round := 0; round < 2; round++ {
		p, err := Prepare(second, nil)
		if err != nil {
			t.Fatal(err)
		}
		pushable, err := Finish(second, p, placeAll(p, overlay), true)
		if err != nil {
			t.Fatal(err)
		}
		if pushable {
			t.Fatal("overlay must not produce anything to push")
		}
		if head := strings.TrimSpace(gitRun(t, second, "rev-parse", "HEAD")); head != p.Remote {
			t.Fatalf("HEAD = %s, want remote %s", head, p.Remote)
		}
		if round == 0 {
			pushEdit(t, first, "ns/settings", "l1\nl2\nl3-remote\nl4\nl5-remote\n")
		}
	}
	if got := readFileString(t, second, "ns/settings"); got != "l1-local\nl2\nl3-remote\nl4\nl5-remote\n" {
		t.Fatalf("settings = %q, want local edit on top of both remote edits", got)
	}
	if readFileString(t, second, "ns/new") != "junk" {
		t.Fatal("untracked file in overlay namespace lost")
	}
	diff := gitRun(t, second, "diff", "--unified=0", "--", "ns/settings")
	if !strings.Contains(diff, "+l1-local") || strings.Contains(diff, "+l3-remote") || strings.Contains(diff, "-l3") {
		t.Fatalf("worktree diff vs HEAD should be only the local edit:\n%s", diff)
	}
}

func TestPrepare_ConflictReportedPerUnitAndHeldBaseKeepsItDetected(t *testing.T) {
	_, first, second := newReconcileClones(t)
	pushEdit(t, first, "ns/file", "base")
	gitRun(t, second, "pull", "origin", "main")
	pushEdit(t, first, "ns/file", "remote")
	writeReconcileFile(t, second, "ns/file", "local")

	p, err := Prepare(second, nil)
	if err != nil {
		t.Fatal(err)
	}
	u := unitNamed(t, p, "ns")
	if strings.Join(u.Conflicts, ",") != "ns/file" {
		t.Fatalf("conflicts = %v, want [ns/file]", u.Conflicts)
	}
	held := Placement{Commit: SourceRemote, Worktree: SourceUntouched}
	if _, err := Finish(second, p, placeAll(p, held), true); err != nil {
		t.Fatal(err)
	}
	if readFileString(t, second, "ns/file") != "local" {
		t.Fatal("held namespace's file was touched")
	}

	again, err := Prepare(second, map[string]string{"ns": u.Base})
	if err != nil {
		t.Fatal(err)
	}
	if len(unitNamed(t, again, "ns").Conflicts) == 0 {
		t.Fatal("with the held base the conflict must still be detected")
	}
	without, err := Prepare(second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(unitNamed(t, without, "ns").Conflicts) != 0 {
		t.Fatal("fixture check: without the held base the edit looks clean")
	}
}

func TestPrepare_RemoteDeletesLocallyEditedNamespaceConflicts(t *testing.T) {
	_, first, second := newReconcileClones(t)
	pushEdit(t, first, "ns/file", "base")
	gitRun(t, second, "pull", "origin", "main")
	gitRun(t, first, "rm", "-r", "ns")
	gitRun(t, first, "commit", "-m", "drop ns")
	gitRun(t, first, "push", "origin", "main")
	writeReconcileFile(t, second, "ns/file", "local")

	p, err := Prepare(second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(unitNamed(t, p, "ns").Conflicts) == 0 {
		t.Fatal("expected a modify/delete conflict on ns")
	}
}

func TestFinish_LocalPlacementReplacesRemote(t *testing.T) {
	_, first, second := newReconcileClones(t)
	pushEdit(t, first, "ns/file", "base")
	gitRun(t, second, "pull", "origin", "main")
	pushEdit(t, first, "ns/file", "remote")
	writeReconcileFile(t, second, "ns/file", "local")

	p, err := Prepare(second, nil)
	if err != nil {
		t.Fatal(err)
	}
	pl := placeAll(p, Placement{Commit: SourceRemote, Worktree: SourceRemote})
	pl["ns"] = Placement{Commit: SourceLocal, Worktree: SourceUntouched}
	if _, err := Finish(second, p, pl, true); err != nil {
		t.Fatal(err)
	}
	if got := gitRun(t, second, "show", "HEAD:ns/file"); got != "local" {
		t.Fatalf("committed ns/file = %q, want local", got)
	}
	assertSettledCleanly(t, second)
}

func TestFinish_ReadOnlyRefusesToCommit(t *testing.T) {
	_, _, second := newReconcileClones(t)
	writeReconcileFile(t, second, "ns/file", "local")
	p, err := Prepare(second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Finish(second, p, placeAll(p, merged), false); err == nil {
		t.Fatal("expected Finish to refuse a commit when committing is not allowed")
	}
}

func TestFinish_NoRemoteCommitsLocally(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	configureReconcileRepo(t, dir)
	writeReconcileFile(t, dir, "ns/file", "content")

	p, err := Prepare(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.RemoteConfigured {
		t.Fatal("expected no remote")
	}
	pushable, err := Finish(dir, p, placeAll(p, merged), true)
	if err != nil {
		t.Fatal(err)
	}
	if pushable {
		t.Fatal("nothing to push without a remote")
	}
	if got := gitRun(t, dir, "show", "HEAD:ns/file"); got != "content" {
		t.Fatalf("HEAD:ns/file = %q", got)
	}
	assertSettledCleanly(t, dir)
}
