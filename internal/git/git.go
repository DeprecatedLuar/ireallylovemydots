// git.go holds the small, low-level git primitives Reconcile is built
// from: running a command in a repository, reading its current branch,
// detecting an interrupted rebase, and resolving a ref to its commit hash.
// Adapted from dredge's internal/git/git.go, not ported — dotz's version
// drops runGitCommand in favor of streaming commands (fetch, push) through
// gitutil.CappedWriter so a slow network call is never silent; see
// reconcile.go.
package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// gitCmd runs a git command in dir and returns its combined output. Used
// for local, fast commands whose output is either discarded or parsed —
// never for a command that can block on the network, which streams
// through reconcile.go's fetch/Push instead.
func gitCmd(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// getCurrentBranch returns dir's checked-out branch name.
func getCurrentBranch(dir string) (string, error) {
	out, err := gitCmd(dir, "branch", "--show-current")
	if err != nil {
		return "", fmt.Errorf("get current branch in %s: %s", dir, strings.TrimSpace(out))
	}
	return strings.TrimSpace(out), nil
}

// isRebaseInProgress reports whether dir has an interrupted rebase left
// behind by a crash, per concept.md "Sync": "The next run still checks
// for an interrupted rebase, because a crash between the two can leave
// one behind."
func isRebaseInProgress(dir string) bool {
	gitDir := filepath.Join(dir, ".git")
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		if info, err := os.Stat(filepath.Join(gitDir, name)); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

// StagePath stages every change under path within dir's index — additions,
// edits, and deletions alike — scoped so it never touches unrelated
// uncommitted work elsewhere in the repository. `namespace rm` uses this to
// record a namespace's removal in the index immediately, at trash time,
// rather than leaving its absence to be discovered — and possibly
// misread — by a later pass: self-heal's sparse-checkout cone
// reconciliation runs ahead of every subsequent invocation and would
// otherwise have no way to tell an explicit removal apart from a namespace
// folder deleted by hand, which concept.md's "Sparse checkout" says must
// read as merely uninstalled. An already-staged deletion settles that
// ahead of time: reconciling the cone around it — even before the next
// `sync` commits it — never disturbs a staged change, verified in
// repo.ReconcileCone's own tests.
func StagePath(dir, path string) error {
	out, err := gitCmd(dir, "add", "-A", "--", path)
	if err != nil {
		return fmt.Errorf("stage %s in %s: %s", path, dir, strings.TrimSpace(out))
	}
	return nil
}

// resolveRef resolves ref to its commit hash in dir.
func resolveRef(dir, ref string) (string, error) {
	out, err := gitCmd(dir, "rev-parse", "--verify", ref)
	if err != nil {
		return "", fmt.Errorf("resolve %s in %s: %s", ref, dir, strings.TrimSpace(out))
	}
	return strings.TrimSpace(out), nil
}
