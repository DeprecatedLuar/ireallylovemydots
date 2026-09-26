package git

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
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

// Side names which side of a sync conflict wins, from the user's point of
// view (concept.md "Resolving a conflict"): never git's ours/theirs, whose
// meaning inverts during a rebase.
type Side int

const (
	// SideNone resolves nothing: a conflict is reported, never settled.
	SideNone Side = iota
	// SideLocal keeps this machine's version of every conflicting hunk.
	SideLocal
	// SideRemote keeps the remote's version of every conflicting hunk.
	SideRemote
)

// rebaseStrategyOption maps a Side to the `-X` merge strategy option that
// picks it. Git's names invert during a rebase: the branch being rebased onto
// (the remote) is "ours" and the commits being replayed (local) are "theirs",
// so keeping local means `theirs` and keeping remote means `ours`.
var rebaseStrategyOption = map[Side]string{
	SideLocal:  "theirs",
	SideRemote: "ours",
}

// mergeTreeConflictExit is the exit status `git merge-tree` uses to report
// that the merge has conflicts, as opposed to failing outright.
const mergeTreeConflictExit = 1

// Index stages of an unmerged path: 1 is the common base, 2 the side being
// rebased onto, 3 the commit being replayed. A content conflict has all three;
// an add/add conflict has 2 and 3 but no base. A path missing stage 2 or 3 is
// a modify/delete or rename conflict, which a side flag cannot settle.
const (
	stageBase   = 1
	stageOurs   = 2
	stageTheirs = 3
)

// ConflictError reports that local and remote changed the same paths and the
// rebase could not settle them. Paths lists every conflicted path, sorted.
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
// remote — fetches and rebases local commits onto it, per concept.md
// "Sync": "commit everything, fetch, rebase onto the remote, push." It
// never pushes and never reapplies dir's sparse-checkout cone: both are
// the caller's job, in that order, once Reconcile returns cleanly — a
// rebase can materialize paths outside the cone, and reapplying before the
// push is what keeps that from being published as deletions (concept.md
// "Sync": "Sync runs `git sparse-checkout reapply` after rebasing and
// before pushing.").
//
// RemoteConfigured reports whether dir has a remote at all: a repository
// with none (the ordinary result of `repo init`) commits locally and has
// nothing left to do, which is not an error.
//
// A plain rebase is always attempted first. On conflict it is aborted before
// returning so dir's working tree is left clean — dotz cannot leave a rebase
// in progress the way dredge does, because the conflicted path may be
// symlinked live into the user's home (concept.md "Sync": "A stopped rebase
// is aborted before sync returns."). Both states stay reachable through a
// local recovery ref and the remote-tracking branch, and the failure is a
// *ConflictError; the repository is not pushed, which is the caller's
// responsibility to skip on error. When side names a winner and every
// conflict is a content conflict, the rebase is rerun preferring that side
// and the settled paths are reported in Overridden (concept.md "Resolving a
// conflict"). side has no effect when the plain rebase succeeds.
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

	recoveryRef := recoveryRefNamespace + "/" + localHead
	if out, err := gitCmd(dir, "update-ref", recoveryRef, localHead); err != nil {
		return result, fmt.Errorf("preserve local head %s in %s: %s", localHead, dir, strings.TrimSpace(out))
	}

	stopped, err := rebaseAborting(dir, remoteHead, branch)
	if err != nil {
		return result, err
	}
	if stopped == nil {
		_, _ = gitCmd(dir, "update-ref", "-d", recoveryRef)
		return result, nil
	}

	paths, err := conflictedPaths(dir, remoteHead, localHead)
	if err != nil {
		return result, err
	}
	if len(paths) == 0 {
		// merge-tree saw no conflict the rebase did; report what the rebase
		// itself stopped on rather than an empty list.
		paths = stopped.paths
	}
	conflict := &ConflictError{Dir: dir, Branch: branch, Paths: paths, Resolvable: stopped.resolvable}
	if side == SideNone || !stopped.resolvable {
		return result, conflict
	}

	stopped, err = rebaseAborting(dir, remoteHead, branch, "-X", rebaseStrategyOption[side])
	if err != nil {
		return result, err
	}
	if stopped != nil {
		return result, &ConflictError{Dir: dir, Branch: branch, Paths: stopped.paths, Resolvable: false}
	}
	_, _ = gitCmd(dir, "update-ref", "-d", recoveryRef)
	result.Overridden = paths
	return result, nil
}

// stoppedRebase describes a rebase that stopped on conflicts, captured while
// it was still stopped and before it was aborted.
type stoppedRebase struct {
	// paths are the unmerged paths of the commit the rebase stopped on.
	paths []string
	// resolvable is true when every unmerged path is a content conflict.
	resolvable bool
}

// rebaseAborting runs `git rebase [extra...] onto branch`. On success it
// returns a nil *stoppedRebase. On conflict it reads the unmerged index, then
// aborts so the tree is left clean, and returns what it read. A failure to
// abort, or a failure that left no rebase in progress, is an error.
func rebaseAborting(dir, onto, branch string, extra ...string) (*stoppedRebase, error) {
	args := append([]string{"rebase"}, extra...)
	args = append(args, onto, branch)
	out, err := gitCmd(dir, args...)
	if err == nil {
		return nil, nil
	}
	if !isRebaseInProgress(dir) {
		return nil, fmt.Errorf("rebase failed in %s: %s", dir, strings.TrimSpace(out))
	}

	stopped, err := readUnmerged(dir)
	if err != nil {
		_, _ = gitCmd(dir, "rebase", "--abort")
		return nil, err
	}
	if abortOut, abortErr := gitCmd(dir, "rebase", "--abort"); abortErr != nil {
		return nil, fmt.Errorf(
			"sync stopped with conflicts in %s and failed to abort the rebase: %s",
			dir, strings.TrimSpace(abortOut),
		)
	}
	return stopped, nil
}

// readUnmerged reads `git ls-files -u` in dir: every unmerged path, sorted,
// and whether each one carries all three index stages.
func readUnmerged(dir string) (*stoppedRebase, error) {
	out, err := gitCmd(dir, "ls-files", "-u")
	if err != nil {
		return nil, fmt.Errorf("read unmerged paths in %s: %s", dir, strings.TrimSpace(out))
	}
	stages := map[string]map[int]bool{}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		meta, path, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) != 3 {
			return nil, fmt.Errorf("unexpected ls-files -u line in %s: %q", dir, line)
		}
		stage, convErr := strconv.Atoi(fields[2])
		if convErr != nil {
			return nil, fmt.Errorf("unexpected ls-files -u stage in %s: %q", dir, line)
		}
		if stages[path] == nil {
			stages[path] = map[int]bool{}
		}
		stages[path][stage] = true
	}
	stopped := &stoppedRebase{resolvable: len(stages) > 0}
	for path, have := range stages {
		stopped.paths = append(stopped.paths, path)
		if !have[stageOurs] || !have[stageTheirs] {
			stopped.resolvable = false
		}
	}
	sort.Strings(stopped.paths)
	return stopped, nil
}

// conflictedPaths lists every path a merge of localHead into remoteHead
// conflicts on, sorted. It uses merge-tree rather than the stopped rebase so
// conflicts in later local commits are listed too, not only the first commit
// the rebase stopped on. No conflict yields no paths and no error.
func conflictedPaths(dir, remoteHead, localHead string) ([]string, error) {
	cmd := exec.Command("git", "merge-tree", "--write-tree", "--name-only", "--no-messages", remoteHead, localHead)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != mergeTreeConflictExit {
			return nil, fmt.Errorf("list conflicted paths in %s: %w", dir, err)
		}
	} else {
		return nil, nil
	}

	lines := strings.Split(string(out), "\n")
	var paths []string
	// The first line is the resulting tree's OID; paths follow until the
	// first blank line.
	for _, line := range lines[1:] {
		if line == "" {
			break
		}
		paths = append(paths, line)
	}
	sort.Strings(paths)
	return paths, nil
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

	msg, err := commitMessage(dir)
	if err != nil {
		return err
	}
	if out, err := gitCmd(dir, "commit", "-m", msg); err != nil {
		return fmt.Errorf("commit in %s: %s", dir, strings.TrimSpace(out))
	}
	return nil
}

// stagedNamespaceClasses classifies every namespace touched by dir's staged
// changes as added, updated, or removed, per concept.md "Sync": "derived
// from the namespaces the commit touches... no item-level detail, no
// machine name." A namespace is the first path segment of every tracked
// path in a repository (concept.md "Namespace"), so per-namespace
// classification comes from the git status letters seen among its changed
// paths: a namespace whose paths are all newly added reads as added, all
// removed reads as removed, and anything else — including a modification
// or a rename, which carries both an old and a new path — reads as
// updated. Shared by commitMessage, which reports all three, and
// StagedRemovals, which only needs the removed set.
func stagedNamespaceClasses(dir string) (added, updated, removed []string, err error) {
	out, err := gitCmd(dir, "diff", "--cached", "--name-status")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("inspect staged changes in %s: %s", dir, strings.TrimSpace(out))
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
func commitMessage(dir string) (string, error) {
	added, updated, removed, err := stagedNamespaceClasses(dir)
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
	_, _, removed, err := stagedNamespaceClasses(dir)
	return removed, err
}
