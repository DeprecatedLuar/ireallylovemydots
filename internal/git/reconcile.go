package git

import (
	"errors"
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

// Side names which side of a sync conflict wins, from the user's point of
// view (concept.md "Resolving a conflict"): never git's ours/theirs, whose
// meaning depends on which argument a merge was given first.
type Side int

const (
	// SideNone resolves nothing: a conflict is reported, never settled.
	SideNone Side = iota
	// SideLocal keeps this machine's version of every conflicting hunk.
	SideLocal
	// SideRemote keeps the remote's version of every conflicting hunk.
	SideRemote
)

// mergeStrategyOption maps a Side to the `-X` merge strategy option that
// picks it. merge-tree is given the remote first, so the remote is "ours" and
// local is "theirs": keeping local means `theirs`, keeping remote `ours`.
var mergeStrategyOption = map[Side]string{
	SideLocal:  "theirs",
	SideRemote: "ours",
}

// mergeBaseNotAncestorExit is the exit status `git merge-base --is-ancestor`
// uses for "not an ancestor", as opposed to failing outright.
const mergeBaseNotAncestorExit = 1

// ConflictError reports that local and remote changed the same paths and the
// merge could not settle them. Paths lists every conflicted path, sorted.
// Resolvable is true only when every conflict is a content conflict a side
// flag can settle. Callers detect it with errors.As; the command layer does
// the user-facing formatting.
type ConflictError struct {
	Dir        string
	Branch     string
	Paths      []string
	Resolvable bool
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("sync stopped in %s: local and remote both changed %s on %s",
		e.Dir, strings.Join(e.Paths, ", "), e.Branch)
}

// ReconcileResult is what a Reconcile that did not fail reports.
type ReconcileResult struct {
	// RemoteConfigured is false for a repository with no remote.
	RemoteConfigured bool
	// Overridden lists the conflicted paths a side flag settled, sorted.
	Overridden []string
}

// pushAuthFailureMarkers are substrings git's push stderr is known to carry
// when a push is rejected for lack of credentials, matched case-sensitively
// against the exact wording git and the common forges (GitHub, GitLab,
// Bitbucket) use for authentication and authorization rejections over both
// SSH and HTTPS. Kept narrow on purpose, per concept.md "Read-only
// repositories": "a push that fails for any other reason, or ambiguously,
// changes nothing" — a marker only belongs here when it is unambiguous.
var pushAuthFailureMarkers = []string{
	"Permission denied (publickey)",
	"Authentication failed",
	"authentication failed",
	"could not read Username",
	"could not read Password",
	"Access denied",
}

// Reconcile commits every tracked change in dir, then — if dir has a
// remote — fetches and merges remote and local in memory, per concept.md
// "Sync". It never pushes and never reapplies dir's sparse-checkout cone:
// both are the caller's job, in that order, once Reconcile returns cleanly —
// moving the working tree can materialize paths outside the cone, and
// reapplying before the push is what keeps that from being published as
// deletions (concept.md "Sync": "Sync runs `git sparse-checkout reapply`
// after applying the result and before pushing.").
//
// RemoteConfigured reports whether dir has a remote at all: a repository
// with none (the ordinary result of `repo init`) commits locally and has
// nothing left to do, which is not an error.
//
// Sync never writes an intermediate state into the working tree, because a
// conflicted path may be symlinked live into the user's home and a watching
// application can read it (concept.md "Sync"). After fetch:
//   - only the remote moved: `reset --keep` straight to the remote head.
//   - only local moved: nothing to apply.
//   - diverged: `git merge-tree` merges both sides in memory. A conflict with
//     no side named, or one a side flag cannot settle, is a *ConflictError and
//     nothing moved. Otherwise the merged tree is committed on top of the
//     remote as one commit and the working tree moves to it in one
//     `reset --keep`. When side names a winner and every conflict is a
//     content conflict, the merge is rerun preferring that side and the
//     settled paths are reported in Overridden (concept.md "Resolving a
//     conflict"). side has no effect when the plain merge is clean.
//
// The repository is not pushed on a conflict, which is the caller's
// responsibility to skip on error.
func Reconcile(dir string, side Side) (ReconcileResult, error) {
	if isRebaseInProgress(dir) {
		if out, err := gitCmd(dir, "rebase", "--abort"); err != nil {
			return ReconcileResult{}, fmt.Errorf("recover interrupted sync in %s: %s", dir, strings.TrimSpace(out))
		}
	}

	if err := commitTrackedChanges(dir); err != nil {
		return ReconcileResult{}, err
	}

	remoteConfigured, err := hasRemote(dir)
	if err != nil {
		return ReconcileResult{}, err
	}
	if !remoteConfigured {
		return ReconcileResult{}, nil
	}
	result := ReconcileResult{RemoteConfigured: true}

	branch, err := getCurrentBranch(dir)
	if err != nil || branch == "" {
		return result, fmt.Errorf("sync requires an attached branch in %s", dir)
	}

	remoteHeads, err := gitCmd(dir, "ls-remote", "--heads", remoteName, "refs/heads/"+branch)
	if err != nil {
		return result, fmt.Errorf("inspect remote for %s: %s", dir, strings.TrimSpace(remoteHeads))
	}
	if strings.TrimSpace(remoteHeads) == "" {
		// A new empty remote has nothing to reconcile; the caller's push
		// establishes the branch.
		return result, nil
	}

	if err := fetch(dir, branch); err != nil {
		return result, err
	}

	remoteRef := "refs/remotes/" + remoteName + "/" + branch
	localHead, err := resolveRef(dir, "HEAD")
	if err != nil {
		return result, fmt.Errorf("record local HEAD in %s: %w", dir, err)
	}
	remoteHead, err := resolveRef(dir, remoteRef)
	if err != nil {
		return result, fmt.Errorf("remote branch %s/%s was not found after fetch: %w", remoteName, branch, err)
	}
	if localHead == remoteHead {
		return result, nil
	}

	localBehind, err := isAncestor(dir, localHead, remoteHead)
	if err != nil {
		return result, err
	}
	if localBehind {
		return result, resetKeep(dir, remoteHead)
	}
	remoteBehind, err := isAncestor(dir, remoteHead, localHead)
	if err != nil {
		return result, err
	}
	if remoteBehind {
		return result, nil
	}

	return result, fmt.Errorf("diverged sync not available in %s", dir)
}

// isAncestor reports whether ancestor is reachable from descendant in dir.
// `merge-base --is-ancestor` exits 1 for "no"; any other failure is an error.
func isAncestor(dir, ancestor, descendant string) (bool, error) {
	out, err := gitCmd(dir, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == mergeBaseNotAncestorExit {
		return false, nil
	}
	return false, fmt.Errorf("compare %s and %s in %s: %s", ancestor, descendant, dir, strings.TrimSpace(out))
}

// resetKeep moves dir's branch and working tree to commit in one step,
// writing only the files that differ and refusing to overwrite local edits.
func resetKeep(dir, commit string) error {
	if out, err := gitCmd(dir, "reset", "--keep", commit); err != nil {
		return fmt.Errorf("apply sync result in %s: %s", dir, strings.TrimSpace(out))
	}
	return nil
}

// ErrNotFastForwardable is returned by FastForward when dir's local branch
// has diverged from its upstream and cannot be moved onto it without a
// commit, rebase, or push — none of which a read-only repository's fetch-
// only sync is allowed to do, per concept.md "Read-only repositories".
var ErrNotFastForwardable = errors.New("local branch has diverged and cannot be fast-forwarded")

// ErrPushAuthFailed is returned by Push when the remote rejected the push
// for lack of credentials, distinguished from any other push failure by
// matching pushAuthFailureMarkers against the push's stderr. Callers use
// this to flag a repository read-only, per concept.md "Read-only
// repositories": "A push rejected for authentication flags the repository
// read-only... a push that fails for any other reason, or ambiguously,
// changes nothing."
var ErrPushAuthFailed = errors.New("push rejected for authentication")

// FastForward fetches dir's current branch's upstream and fast-forwards to
// it. It never commits, never rebases, and never pushes — the fetch-only
// sync path for a repository this machine cannot push to (concept.md
// "Read-only repositories"). Returns ErrNotFastForwardable when the local
// branch has diverged from its upstream; the caller is expected to have
// already ruled out uncommitted local changes via Status, since FastForward
// only resolves the branch history, not the working tree.
func FastForward(dir string) (remoteConfigured bool, err error) {
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
		// A new empty remote has nothing to fast-forward onto.
		return true, nil
	}

	if err := fetch(dir, branch); err != nil {
		return true, err
	}

	if out, err := gitCmd(dir, "merge", "--ff-only", remoteName+"/"+branch); err != nil {
		return true, fmt.Errorf("%w: %s: %s", ErrNotFastForwardable, dir, strings.TrimSpace(out))
	}
	return true, nil
}

// Push pushes dir's current branch to origin, streaming output live
// through gitutil.CappedWriter so a slow push is never silent. A failure
// rejected for authentication is wrapped in ErrPushAuthFailed so callers can
// tell it apart from every other kind of push failure.
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
		msg := strings.TrimSpace(tail.String())
		if isPushAuthFailure(msg) {
			return fmt.Errorf("push %s: %s: %w", dir, msg, ErrPushAuthFailed)
		}
		return fmt.Errorf("push %s: %s", dir, msg)
	}
	return nil
}

// isPushAuthFailure reports whether a push's stderr names an authentication
// or authorization rejection, per pushAuthFailureMarkers.
func isPushAuthFailure(stderr string) bool {
	for _, marker := range pushAuthFailureMarkers {
		if strings.Contains(stderr, marker) {
			return true
		}
	}
	return false
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
// deletions for them." That guarantee covers a namespace correctly
// excluded from the cone — it does not extend to a working tree that has
// drifted from the cone (a namespace folder present but outside it, or a
// name in the cone with no folder on disk): `git add -A` exits nonzero for
// the former and would otherwise stage the latter's namespace as a
// deletion to commit. Sync relies on self-heal's cone reconciliation
// (internal/repo.ReconcileCone, run ahead of every invocation) to have
// already settled that before this ever runs.
func commitTrackedChanges(dir string) error {
	if out, err := gitCmd(dir, "add", "-A"); err != nil {
		return fmt.Errorf("stage changes in %s: %s", dir, strings.TrimSpace(out))
	}
	return commitStaged(dir)
}

// commitStaged commits whatever is currently staged in dir, deriving the
// message from commitMessage. A clean index is a no-op, not an empty
// commit. Shared by commitTrackedChanges and RewindAndRecommit, which both
// end the same way: something is staged, and it needs one commit on top of
// wherever HEAD currently points.
func commitStaged(dir string) error {
	if _, err := gitCmd(dir, "diff", "--cached", "--quiet"); err == nil {
		return nil
	}

	msg, err := commitMessage(dir, "--cached")
	if err != nil {
		return err
	}
	if out, err := gitCmd(dir, "commit", "-m", msg); err != nil {
		return fmt.Errorf("commit in %s: %s", dir, strings.TrimSpace(out))
	}
	return nil
}

// namespaceClasses classifies every namespace touched by the changes
// `git diff diffArgs...` reports in dir as added, updated, or removed, per
// concept.md "Sync": "derived from the namespaces the commit touches... no
// item-level detail, no machine name." A namespace is the first path segment of every tracked
// path in a repository (concept.md "Namespace"), so per-namespace
// classification comes from the git status letters seen among its changed
// paths: a namespace whose paths are all newly added reads as added, all
// removed reads as removed, and anything else — including a modification
// or a rename, which carries both an old and a new path — reads as
// updated. Shared by commitMessage, which reports all three, and
// StagedRemovals, which only needs the removed set.
func namespaceClasses(dir string, diffArgs ...string) (added, updated, removed []string, err error) {
	out, err := gitCmd(dir, append([]string{"diff", "--name-status"}, diffArgs...)...)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("inspect changes in %s: %s", dir, strings.TrimSpace(out))
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
	return added, updated, removed, nil
}

// commitMessage summarizes dir's staged change by the namespaces it
// touches, per concept.md "Sync": "derived from the namespaces the commit
// touches... no item-level detail, no machine name."
func commitMessage(dir string, diffArgs ...string) (string, error) {
	added, updated, removed, err := namespaceClasses(dir, diffArgs...)
	if err != nil {
		return "", err
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

// StagedRemovals returns the namespaces whose staged changes are entirely
// deletions — namespaces `rm` has already trashed and staged (see
// StagePath) but that sync has not yet committed. Sync reads this ahead of
// committing to decide whether any of them needs RewindAndRecommit instead
// of an ordinary commit.
func StagedRemovals(dir string) ([]string, error) {
	_, _, removed, err := namespaceClasses(dir, "--cached")
	return removed, err
}
