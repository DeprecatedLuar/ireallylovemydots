package git

import (
	"strings"
	"testing"
)

func TestSnapshotTree_CapturesEditsAndNewFilesWithoutTouchingIndex(t *testing.T) {
	_, _, second := newReconcileClones(t)
	writeReconcileFile(t, second, "seed", "edited")
	writeReconcileFile(t, second, "ns/new", "new")

	tree, err := snapshotTree(second)
	if err != nil {
		t.Fatal(err)
	}
	if got := gitRun(t, second, "cat-file", "-p", tree+":ns/new"); got != "new" {
		t.Fatalf("ns/new in snapshot = %q", got)
	}
	if got := gitRun(t, second, "cat-file", "-p", tree+":seed"); got != "edited" {
		t.Fatalf("seed in snapshot = %q", got)
	}
	if staged := strings.TrimSpace(gitRun(t, second, "diff", "--cached", "--name-only")); staged != "" {
		t.Fatalf("real index changed: %s", staged)
	}
}

func TestSnapshotTree_SparseCloneDoesNotDropUninstalledNamespaces(t *testing.T) {
	_, first, second := newReconcileClones(t)
	writeReconcileFile(t, first, "kept/file", "k")
	writeReconcileFile(t, first, "away/file", "a")
	gitRun(t, first, "add", "-A")
	gitRun(t, first, "commit", "-m", "two namespaces")
	gitRun(t, first, "push", "origin", "main")
	gitRun(t, second, "pull", "origin", "main")
	gitRun(t, second, "sparse-checkout", "set", "kept")

	tree, err := snapshotTree(second)
	if err != nil {
		t.Fatal(err)
	}
	if got := gitRun(t, second, "cat-file", "-p", tree+":away/file"); got != "a" {
		t.Fatalf("uninstalled namespace missing from snapshot: %q", got)
	}
}

func TestTopLevelAndMkTree_RoundTrip(t *testing.T) {
	_, first, _ := newReconcileClones(t)
	writeReconcileFile(t, first, "ns/file", "x")
	gitRun(t, first, "add", "-A")
	gitRun(t, first, "commit", "-m", "ns")

	entries, err := topLevel(first, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if !isTreeLine(entries["ns"]) || isTreeLine(entries["seed"]) {
		t.Fatalf("entries = %v, want ns a tree and seed a blob", entries)
	}
	tree, err := mkTree(first, []string{entries["ns"]})
	if err != nil {
		t.Fatal(err)
	}
	if got := gitRun(t, first, "cat-file", "-p", tree+":ns/file"); got != "x" {
		t.Fatalf("ns/file = %q", got)
	}
	empty, err := mkTree(first, nil)
	if err != nil || empty != emptyTree {
		t.Fatalf("mkTree(nil) = %q, %v; want the empty tree", empty, err)
	}
}

func TestHeadCommit_UnbornIsEmpty(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	head, err := headCommit(dir)
	if err != nil || head != "" {
		t.Fatalf("headCommit = %q, %v; want empty", head, err)
	}
}

func TestMergeTree_ConflictStillReturnsTreeAndPaths(t *testing.T) {
	_, first, second := newReconcileClones(t)
	writeReconcileFile(t, first, "seed", "remote")
	writeReconcileFile(t, first, "other", "remote other")
	gitRun(t, first, "add", "-A")
	gitRun(t, first, "commit", "-m", "remote")
	gitRun(t, first, "push", "origin", "main")
	gitRun(t, second, "fetch", "origin", "main")
	writeReconcileFile(t, second, "seed", "local")
	gitRun(t, second, "add", "-A")
	gitRun(t, second, "commit", "-m", "local")

	tree, conflicts, err := mergeTree(second, "origin/main", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if tree == "" || strings.Join(conflicts, ",") != "seed" {
		t.Fatalf("tree=%q conflicts=%v; want a tree and [seed]", tree, conflicts)
	}
	if got := gitRun(t, second, "cat-file", "-p", tree+":other"); got != "remote other" {
		t.Fatalf("non-conflicted path in merged tree = %q", got)
	}
}
