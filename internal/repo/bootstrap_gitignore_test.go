package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPlanGitignoreLines_NegationPreservesBang covers the negation carry-
// through from concept.md's move-map table: "!copyq/keep" rewrites to
// "!copyq/copyq/keep", the "!" stripped, the rule applied, and restored.
func TestPlanGitignoreLines_NegationPreservesBang(t *testing.T) {
	plan := []PlannedNamespace{{Namespace: "copyq", EntryName: "copyq"}}
	changes := PlanGitignoreLines([]string{"!copyq/keep"}, plan)
	if len(changes) != 1 {
		t.Fatalf("expected exactly one change, got %+v", changes)
	}
	if changes[0].Outcome != GitignoreRewritten || changes[0].Rewritten != "!copyq/copyq/keep" {
		t.Fatalf("expected negation preserved through the rewrite, got %+v", changes[0])
	}
}

// TestGitignoreRewrite_RootEntryDirectory_LockFileStillIgnoredAfterBootstrap
// is concept.md's worked example end to end: copyq/ becomes the namespace
// copyq holding the entry copyq (so copyq/copyq.lock moves to
// copyq/copyq/copyq.lock), a slashless *.lock pattern is left alone, and a
// stale config/-rooted pattern with no matching converted entry is reported
// unmapped rather than silently dropped or kept. The concrete proof is
// `git check-ignore` against the file's new location after Apply, not just
// the rewritten text.
func TestGitignoreRewrite_RootEntryDirectory_LockFileStillIgnoredAfterBootstrap(t *testing.T) {
	dir := initGitRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, "copyq"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "copyq", "copyq.conf"), "config")
	writeFile(t, filepath.Join(dir, ".gitignore"), "copyq/copyq.lock\n*.lock\nconfig/copyq/copyq.lock\n")
	commitAll(t, dir)

	// Written only after .gitignore is committed, so it starts out ignored —
	// exactly the file the rewrite exists to keep ignored.
	writeFile(t, filepath.Join(dir, "copyq", "copyq.lock"), "lock")
	if got := gitStatusPorcelain(t, dir); got != "" {
		t.Fatalf("expected the lock file to be ignored before bootstrap, got dirty status: %q", got)
	}

	entries, err := RootEntries(dir)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanEntries(entries)
	if err != nil {
		t.Fatal(err)
	}

	changes, err := PlanGitignore(dir, plan)
	if err != nil {
		t.Fatal(err)
	}
	var rewritten, unchanged, unmapped []GitignoreChange
	for _, c := range changes {
		switch c.Outcome {
		case GitignoreRewritten:
			rewritten = append(rewritten, c)
		case GitignoreUnchanged:
			if strings.TrimSpace(c.Original) != "" {
				unchanged = append(unchanged, c)
			}
		case GitignoreUnmapped:
			unmapped = append(unmapped, c)
		}
	}
	if len(rewritten) != 1 || rewritten[0].Original != "copyq/copyq.lock" || rewritten[0].Rewritten != "copyq/copyq/copyq.lock" {
		t.Fatalf("expected copyq/copyq.lock rewritten to copyq/copyq/copyq.lock, got %+v", rewritten)
	}
	if len(unchanged) != 1 || unchanged[0].Original != "*.lock" {
		t.Fatalf("expected *.lock left unchanged, got %+v", unchanged)
	}
	if len(unmapped) != 1 || unmapped[0].Original != "config/copyq/copyq.lock" {
		t.Fatalf("expected config/copyq/copyq.lock reported unmapped, got %+v", unmapped)
	}

	if err := Apply(dir, plan); err != nil {
		t.Fatal(err)
	}

	gitignoreContent, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(gitignoreContent)
	if !strings.Contains(text, "copyq/copyq/copyq.lock") {
		t.Fatalf("expected the rewritten .gitignore to hold copyq/copyq/copyq.lock, got:\n%s", text)
	}
	if !strings.Contains(text, "*.lock") {
		t.Fatalf("expected *.lock preserved in the rewritten .gitignore, got:\n%s", text)
	}
	if !strings.Contains(text, "config/copyq/copyq.lock") {
		t.Fatalf("expected the unmapped pattern preserved verbatim, got:\n%s", text)
	}

	newLockPath := filepath.Join("copyq", "copyq", "copyq.lock")
	if _, err := os.Stat(filepath.Join(dir, newLockPath)); err != nil {
		t.Fatalf("expected the lock file moved to %s by Apply: %v", newLockPath, err)
	}
	cmd := exec.Command("git", "-C", dir, "check-ignore", "-q", newLockPath)
	if err := cmd.Run(); err != nil {
		t.Fatalf("expected %s to still be gitignored after bootstrap, git check-ignore failed: %v", newLockPath, err)
	}
}

// TestGitignoreRewrite_TrailingSlashOnlyPatternStaysUnrooted covers the real
// correctness bug the code review found: gitignore(5) roots a pattern to
// the .gitignore's own directory only when it has a slash at the beginning
// or in the middle — a slash that appears ONLY at the end ("build/") does
// NOT root it, and it still matches at any depth, exactly like a slashless
// pattern such as "*.lock". The old code treated "contains a slash
// anywhere" as rooted, so "build/" got the namespace-prefix rewrite
// treatment and silently stopped matching outside the "build" namespace's
// own directory (e.g. "other/build/"). This test proves, with a real git
// repository and `git check-ignore`, that "build/" keeps ignoring a build
// directory living under an unrelated, also-converted root entry both
// before and after bootstrap, while a genuinely rooted pattern naming the
// namespace itself ("build/cache", a slash in the middle) still gets
// rewritten to its namespaced form.
func TestGitignoreRewrite_TrailingSlashOnlyPatternStaysUnrooted(t *testing.T) {
	dir := initGitRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, "build"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "other"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "build", "build.conf"), "config")
	writeFile(t, filepath.Join(dir, "other", "other.conf"), "config")
	commitAll(t, dir)

	// The .gitignore is added and committed only after "build" and "other"
	// are already tracked, exactly like the real repository the bug report
	// describes: git does not retroactively untrack a directory just
	// because a later-added pattern would now match it, which is what lets
	// a root entry named "build" coexist with a "build/" ignore pattern at
	// all.
	writeFile(t, filepath.Join(dir, ".gitignore"), "build/\nbuild/cache\n")
	commitAll(t, dir)

	// Written only after .gitignore is committed, so both start out ignored:
	// build/cache via the rooted pattern naming it directly, other/build via
	// the trailing-slash-only pattern matching "build" at any depth.
	if err := os.MkdirAll(filepath.Join(dir, "build", "cache"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "build", "cache", "data"), "cache-data")
	if err := os.MkdirAll(filepath.Join(dir, "other", "build"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "other", "build", "data"), "other-build-data")
	if got := gitStatusPorcelain(t, dir); got != "" {
		t.Fatalf("expected both paths ignored before bootstrap, got dirty status: %q", got)
	}

	entries, err := RootEntries(dir)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanEntries(entries)
	if err != nil {
		t.Fatal(err)
	}

	changes, err := PlanGitignore(dir, plan)
	if err != nil {
		t.Fatal(err)
	}
	var trailingSlashChange, rootedChange *GitignoreChange
	for i, c := range changes {
		switch c.Original {
		case "build/":
			trailingSlashChange = &changes[i]
		case "build/cache":
			rootedChange = &changes[i]
		}
	}
	if trailingSlashChange == nil || trailingSlashChange.Outcome != GitignoreUnchanged || trailingSlashChange.Rewritten != "build/" {
		t.Fatalf("expected trailing-slash-only \"build/\" left unchanged (not rooted), got %+v", trailingSlashChange)
	}
	if rootedChange == nil || rootedChange.Outcome != GitignoreRewritten || rootedChange.Rewritten != "build/build/cache" {
		t.Fatalf("expected \"build/cache\" (slash in the middle) rewritten to \"build/build/cache\", got %+v", rootedChange)
	}

	if err := Apply(dir, plan); err != nil {
		t.Fatal(err)
	}

	// build's own cache dir moved under the new namespace folder; other's
	// build dir moved along with the rest of "other" into its own namespace
	// folder, landing outside "build/"'s namespace entirely.
	rootedPath := filepath.Join("build", "build", "cache", "data")
	if _, err := os.Stat(filepath.Join(dir, rootedPath)); err != nil {
		t.Fatalf("expected %s to exist after Apply: %v", rootedPath, err)
	}
	unrelatedPath := filepath.Join("other", "other", "build", "data")
	if _, err := os.Stat(filepath.Join(dir, unrelatedPath)); err != nil {
		t.Fatalf("expected %s to exist after Apply: %v", unrelatedPath, err)
	}

	if err := exec.Command("git", "-C", dir, "check-ignore", "-q", rootedPath).Run(); err != nil {
		t.Fatalf("expected %s to still be ignored after bootstrap via the rewritten rooted pattern: %v", rootedPath, err)
	}
	if err := exec.Command("git", "-C", dir, "check-ignore", "-q", unrelatedPath).Run(); err != nil {
		t.Fatalf("expected %s to still be ignored after bootstrap via the untouched trailing-slash-only pattern: %v", unrelatedPath, err)
	}
}

// TestRewriteGitignore_NoFileIsANoOp covers a bootstrapped repository with
// no root .gitignore at all: Apply must not create one out of nothing.
func TestRewriteGitignore_NoFileIsANoOp(t *testing.T) {
	dir := t.TempDir()
	plan := []PlannedNamespace{{Namespace: "copyq", EntryName: "copyq"}}
	changes, err := RewriteGitignore(dir, plan)
	if err != nil {
		t.Fatal(err)
	}
	if changes != nil {
		t.Fatalf("expected no changes when there is no .gitignore, got %+v", changes)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); !os.IsNotExist(err) {
		t.Fatalf("expected RewriteGitignore not to create a .gitignore, got err=%v", err)
	}
}
