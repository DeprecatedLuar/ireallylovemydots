// rewind.go answers the one question removal needs that reconcile.go and
// status.go don't: whether a namespace's content, once staged for
// deletion, still exists anywhere it can be unpublished from — and, when
// it can, performs that unpublish. Fact-finding and mechanism only, same
// as the rest of this package; the decision to call RewindAndRecommit
// belongs to the caller (sync).
package git

import (
	"fmt"
	"strings"
)

// UnpushedCommitsTouching returns, oldest first, the hashes of commits on
// HEAD not yet reachable from dir's upstream whose diff touches any of
// paths. Empty (with no error) when dir has no upstream configured — there
// is nothing to compare against, matching unpushedCount's convention in
// status.go.
func UnpushedCommitsTouching(dir string, paths []string) ([]string, error) {
	args := append([]string{"rev-list", "--reverse", "@{u}..HEAD", "--"}, paths...)
	out, err := gitCmd(dir, args...)
	if err != nil {
		return nil, nil
	}
	var hashes []string
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		if line != "" {
			hashes = append(hashes, line)
		}
	}
	return hashes, nil
}

// PushedCommitsTouching returns how many commits already reachable from
// dir's upstream touch any of paths — content already published, which a
// local rewind cannot retract. Zero (with no error) when dir has no
// upstream configured, matching UnpushedCommitsTouching.
func PushedCommitsTouching(dir string, paths []string) (int, error) {
	args := append([]string{"rev-list", "--count", "@{u}", "--"}, paths...)
	out, err := gitCmd(dir, args...)
	if err != nil {
		return 0, nil
	}
	var n int
	if _, scanErr := fmt.Sscanf(strings.TrimSpace(out), "%d", &n); scanErr != nil {
		return 0, nil
	}
	return n, nil
}

// RewindTarget returns the commit RewindAndRecommit should reset dir to in
// order to drop paths' content from history entirely: the parent of the
// earliest unpushed commit touching any of them. Errors when no unpushed
// commit touches paths — the caller has nothing to rewind past.
func RewindTarget(dir string, paths []string) (string, error) {
	commits, err := UnpushedCommitsTouching(dir, paths)
	if err != nil {
		return "", err
	}
	if len(commits) == 0 {
		return "", fmt.Errorf("no unpushed commit in %s touches %s", dir, strings.Join(paths, ", "))
	}
	return resolveRef(dir, commits[0]+"^")
}

// RewindAndRecommit moves dir's branch back to target and recommits the
// current working tree as one new commit on top of it, via commitStaged —
// the same message derivation commitTrackedChanges uses. target's own
// history is untouched; only the commits between target and dir's previous
// HEAD are collapsed away, so their content (and only theirs) never
// reaches the remote. `reset --soft` leaves the index and working tree
// alone, so every namespace not being rewound carries its current content
// forward unchanged.
func RewindAndRecommit(dir, target string) error {
	if out, err := gitCmd(dir, "add", "-A"); err != nil {
		return fmt.Errorf("stage changes in %s: %s", dir, strings.TrimSpace(out))
	}
	if out, err := gitCmd(dir, "reset", "--soft", target); err != nil {
		return fmt.Errorf("rewind %s to %s: %s", dir, target, strings.TrimSpace(out))
	}
	return commitStaged(dir)
}
