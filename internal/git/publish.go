package git

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strings"

	"github.com/DeprecatedLuar/ireallylovemydots/internal/gitutil"
)

// remoteName is the only remote sync ever talks to — the one repo.Clone
// and repo init's `git remote add` both name origin.
const remoteName = "origin"

// Steps a network failure is reported under, per concept.md "Sync output".
const (
	StepFetch = "fetch failed"
	StepPush  = "push failed"
)

// remotePrefix starts a line the server sent, as git relays it.
const remotePrefix = "remote:"

// stockLines are git's lines that only restate a failure.
var stockLines = []string{
	"fatal: Could not read from remote repository.",
	"Please make sure you have the correct access rights",
	"and the repository exists.",
}

// promptEnv keeps git from prompting where nobody can see the question.
var promptEnv = []string{"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never"}

// NetError is a fetch or push that failed: the step, and git's output with
// its stock lines dropped.
type NetError struct {
	Step   string
	Output string
	auth   bool
}

func (e *NetError) Error() string { return e.Step + ": " + e.Output }

// Unwrap exposes ErrPushAuthFailed for a push rejected for credentials.
func (e *NetError) Unwrap() error {
	if e.auth {
		return ErrPushAuthFailed
	}
	return nil
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

// ErrPushAuthFailed is returned by Push when the remote rejected the push
// for lack of credentials, distinguished from any other push failure by
// matching pushAuthFailureMarkers against the push's stderr. Callers use
// this to flag a repository read-only, per concept.md "Read-only
// repositories": "A push rejected for authentication flags the repository
// read-only... a push that fails for any other reason, or ambiguously,
// changes nothing."
var ErrPushAuthFailed = errors.New("push rejected for authentication")

// Push pushes dir's current branch to origin and returns the server's
// notes. A rejection for credentials unwraps to ErrPushAuthFailed.
func Push(dir string, live bool) ([]string, error) {
	branch, err := getCurrentBranch(dir)
	if err != nil || branch == "" {
		return nil, fmt.Errorf("push requires an attached branch in %s", dir)
	}
	stderr, err := runNet(dir, live, "push", "-u", remoteName, branch)
	if err != nil {
		return nil, &NetError{Step: StepPush, Output: failureOutput(stderr, err), auth: isPushAuthFailure(stderr)}
	}
	return remoteNotes(stderr), nil
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

// fetch fetches origin's branches and returns the server's notes.
func fetch(dir string, live bool) ([]string, error) {
	stderr, err := runNet(dir, live, "fetch", "--prune", remoteName)
	if err != nil {
		return nil, &NetError{Step: StepFetch, Output: failureOutput(stderr, err)}
	}
	return remoteNotes(stderr), nil
}

// runNet runs a network git command in dir and returns its stderr. live
// streams it to the terminal and leaves git free to prompt; otherwise it is
// captured and prompting is off.
func runNet(dir string, live bool, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var tail gitutil.CappedWriter
	if live {
		cmd.Stdout = os.Stdout
		cmd.Stderr = io.MultiWriter(os.Stderr, &tail)
	} else {
		cmd.Stderr = &tail
		cmd.Env = withPromptsOff(os.Environ())
	}
	err := cmd.Run()
	return tail.String(), err
}

// withPromptsOff appends each promptEnv entry env does not already set.
func withPromptsOff(env []string) []string {
	out := env
	for _, kv := range promptEnv {
		key, _, _ := strings.Cut(kv, "=")
		set := slices.ContainsFunc(env, func(e string) bool { return strings.HasPrefix(e, key+"=") })
		if !set {
			out = append(out, kv)
		}
	}
	return out
}

// failureOutput is what a failed network command shows: its cleaned
// stderr, or the run error when git wrote nothing.
func failureOutput(stderr string, err error) string {
	if out := cleanOutput(stderr); out != "" {
		return out
	}
	return err.Error()
}

// cleanOutput drops blank and stock lines from git's stderr and writes
// $HOME as ~.
func cleanOutput(stderr string) string {
	home := os.Getenv("HOME")
	var kept []string
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimRight(line, " \r")
		if line == "" || slices.Contains(stockLines, strings.TrimSpace(line)) {
			continue
		}
		if home != "" {
			line = strings.ReplaceAll(line, home, "~")
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// remoteNotes returns the server's non-empty lines from git's stderr,
// trailing padding trimmed.
func remoteNotes(stderr string) []string {
	var notes []string
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimRight(line, " \r")
		if strings.HasPrefix(line, remotePrefix) && strings.TrimSpace(strings.TrimPrefix(line, remotePrefix)) != "" {
			notes = append(notes, line)
		}
	}
	return notes
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
