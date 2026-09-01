package git

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/DeprecatedLuar/dotz/internal/gitutil"
)

// remoteName is the only remote sync ever talks to — the one repo.Clone
// and repo init's `git remote add` both name origin.
const remoteName = "origin"

// recoveryRefNamespace is where Reconcile parks the local commit a rebase
// is about to move away from, so it stays reachable even if the rebase
// stops with conflicts. Deleted once the rebase completes without needing
// it. Per concept.md "Sync" and implementation-plan.md's Phase 9 scope,
// parameterized here rather than hardcoded inline.
const recoveryRefNamespace = "refs/dots/sync"

// Reconcile commits every tracked change in dir, then — if dir has a
// remote — fetches and rebases local commits onto it, per concept.md
// "Sync": "commit everything, fetch, rebase onto the remote, push." It
// never pushes and never reapplies dir's sparse-checkout cone: both are
// the caller's job, in that order, once Reconcile returns cleanly — a
// rebase can materialize paths outside the cone, and reapplying before the
// push is what keeps that from being published as deletions (concept.md
// "Sync": "Sync runs `git sparse-checkout reapply` after rebasing and
// before pushing.").
//
// hasRemote reports whether dir has a remote at all: a repository with
// none (the ordinary result of `repo init`) commits locally and has
// nothing left to do, which is not an error.
//
// On conflict, the rebase is aborted before returning so dir's working
// tree is left clean — dotz cannot leave a rebase in progress the way
// dredge does, because the conflicted path may be symlinked live into the
// user's home (concept.md "Sync": "A stopped rebase is aborted before
// sync returns."). The returned error names a local recovery ref, still
// reachable alongside the untouched remote-tracking branch, and dir; the
// repository is not pushed, which is the caller's responsibility to skip
// on error.
func Reconcile(dir string) (remoteConfigured bool, err error) {
	if isRebaseInProgress(dir) {
		if out, err := gitCmd(dir, "rebase", "--abort"); err != nil {
			return false, fmt.Errorf("recover interrupted sync in %s: %s", dir, strings.TrimSpace(out))
		}
	}

	if err := commitTrackedChanges(dir); err != nil {
		return false, err
	}

	remoteConfigured, err = hasRemote(dir)
	if err != nil {
		return false, err
	}
	if !remoteConfigured {
		return false, nil
	}

	branch, err := getCurrentBranch(dir)
	if err != nil || branch == "" {
		return true, fmt.Errorf("sync requires an attached branch in %s", dir)
	}

	remoteHeads, err := gitCmd(dir, "ls-remote", "--heads", remoteName, "refs/heads/"+branch)
	if err != nil {
		return true, fmt.Errorf("inspect remote for %s: %s", dir, strings.TrimSpace(remoteHeads))
	}
	if strings.TrimSpace(remoteHeads) == "" {
		// A new empty remote has nothing to reconcile; the caller's push
		// establishes the branch.
		return true, nil
	}

	if err := fetch(dir, branch); err != nil {
		return true, err
	}

	remoteRef := "refs/remotes/" + remoteName + "/" + branch
	localHead, err := resolveRef(dir, "HEAD")
	if err != nil {
		return true, fmt.Errorf("record local HEAD in %s: %w", dir, err)
	}
	remoteHead, err := resolveRef(dir, remoteRef)
	if err != nil {
		return true, fmt.Errorf("remote branch %s/%s was not found after fetch: %w", remoteName, branch, err)
	}
	if localHead == remoteHead {
		return true, nil
	}

	recoveryRef := recoveryRefNamespace + "/" + localHead
	if out, err := gitCmd(dir, "update-ref", recoveryRef, localHead); err != nil {
		return true, fmt.Errorf("preserve local head %s in %s: %s", localHead, dir, strings.TrimSpace(out))
	}

	if out, err := gitCmd(dir, "rebase", remoteHead, branch); err != nil {
		if _, abortErr := gitCmd(dir, "rebase", "--abort"); abortErr != nil {
			return true, fmt.Errorf(
				"sync stopped with conflicts in %s and failed to abort the rebase: %s",
				dir, strings.TrimSpace(out),
			)
		}
		return true, fmt.Errorf(
			"sync stopped because local and remote changed the same paths; both states are recoverable — local at %s, remote at %s:\n  cd %s\n  git status",
			recoveryRef, remoteRef, dir,
		)
	}

	_, _ = gitCmd(dir, "update-ref", "-d", recoveryRef)
	return true, nil
}

// Push pushes dir's current branch to origin, streaming output live
// through gitutil.CappedWriter so a slow push is never silent.
func Push(dir string) error {
	branch, err := getCurrentBranch(dir)
	if err != nil || branch == "" {
		return fmt.Errorf("push requires an attached branch in %s", dir)
	}
	cmd := exec.Command("git", "push", "-u", remoteName, branch)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	var tail gitutil.CappedWriter
	cmd.Stderr = io.MultiWriter(os.Stderr, &tail)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("push %s: %s", dir, strings.TrimSpace(tail.String()))
	}
	return nil
}

// fetch fetches branch from origin, streaming output live through
// gitutil.CappedWriter so a slow fetch is never silent.
func fetch(dir, branch string) error {
	cmd := exec.Command("git", "fetch", "--prune", remoteName, branch)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	var tail gitutil.CappedWriter
	cmd.Stderr = io.MultiWriter(os.Stderr, &tail)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("fetch %s: %s", dir, strings.TrimSpace(tail.String()))
	}
	return nil
}

// commitTrackedChanges stages every tracked change in dir and commits it,
// per concept.md "Sync": "Committing everything is safe on a partially
// installed repository. Under a cone sparse checkout the uninstalled
// namespaces are marked skip-worktree, and `git add -A` does not stage
// deletions for them." A clean tree is a no-op, not an empty commit.
func commitTrackedChanges(dir string) error {
	if out, err := gitCmd(dir, "add", "-A"); err != nil {
		return fmt.Errorf("stage changes in %s: %s", dir, strings.TrimSpace(out))
	}
	if _, err := gitCmd(dir, "diff", "--cached", "--quiet"); err == nil {
		return nil
	}

	msg, err := commitMessage(dir)
	if err != nil {
		return err
	}
	if out, err := gitCmd(dir, "commit", "-m", msg); err != nil {
		return fmt.Errorf("commit in %s: %s", dir, strings.TrimSpace(out))
	}
	return nil
}

// commitMessage summarizes the staged change by the namespaces it
// touches, classified added / updated / removed, per concept.md "Sync":
// "derived from the namespaces the commit touches... no item-level
// detail, no machine name." A namespace is the first path segment of
// every tracked path in a repository (concept.md "Namespace"), so
// per-namespace classification comes from the git status letters seen
// among its changed paths: a namespace whose paths are all newly added
// reads as added, all removed reads as removed, and anything else —
// including a modification or a rename, which carries both an old and a
// new path — reads as updated.
func commitMessage(dir string) (string, error) {
	out, err := gitCmd(dir, "diff", "--cached", "--name-status")
	if err != nil {
		return "", fmt.Errorf("inspect staged changes in %s: %s", dir, strings.TrimSpace(out))
	}

	statuses := map[string]map[byte]bool{}
	var namespaces []string
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 || len(fields[0]) == 0 {
			continue
		}
		status := fields[0][0]
		for _, path := range fields[1:] {
			ns, _, ok := strings.Cut(path, "/")
			if !ok {
				continue
			}
			if statuses[ns] == nil {
				statuses[ns] = map[byte]bool{}
				namespaces = append(namespaces, ns)
			}
			statuses[ns][status] = true
		}
	}
	sort.Strings(namespaces)

	var added, updated, removed []string
	for _, ns := range namespaces {
		s := statuses[ns]
		switch {
		case len(s) == 1 && s['A']:
			added = append(added, ns)
		case len(s) == 1 && s['D']:
			removed = append(removed, ns)
		default:
			updated = append(updated, ns)
		}
	}

	var parts []string
	if len(added) > 0 {
		parts = append(parts, "add "+strings.Join(added, " "))
	}
	if len(updated) > 0 {
		parts = append(parts, "upd "+strings.Join(updated, " "))
	}
	if len(removed) > 0 {
		parts = append(parts, "del "+strings.Join(removed, " "))
	}
	if len(parts) == 0 {
		return "sync", nil
	}
	return strings.Join(parts, "; "), nil
}
