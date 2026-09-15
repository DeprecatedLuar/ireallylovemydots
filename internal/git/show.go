package git

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// ShowFile returns the bytes of path as committed at rev, for self-heal's
// manifest recovery: a namespace's .dots is a committed, shared file, so
// git's history is a higher-fidelity witness than anything left on this
// machine's disk when the working copy goes missing. found is false when
// path did not exist at rev — a normal case (an uncommitted or brand-new
// namespace), not an error. Existence is checked with cat-file first rather
// than parsing show's stderr, so "not found" is never confused with a real
// git failure.
func ShowFile(repoDir, rev, path string) (data []byte, found bool, err error) {
	ref := rev + ":" + path
	if err := exec.Command("git", "-C", repoDir, "cat-file", "-e", ref).Run(); err != nil {
		return nil, false, nil
	}

	out, err := exec.Command("git", "-C", repoDir, "show", ref).Output()
	if err != nil {
		return nil, false, fmt.Errorf("show %s in %s: %w", ref, repoDir, err)
	}
	return out, true, nil
}

// ListTree returns every file path under prefix at rev in repoDir, relative
// to prefix. Used to read a namespace out of git without materializing it —
// dots cp's path for a source namespace that has never been checked out on
// this machine.
func ListTree(repoDir, rev, prefix string) ([]string, error) {
	out, err := exec.Command("git", "-C", repoDir, "ls-tree", "-r", "--name-only", rev, "--", prefix).Output()
	if err != nil {
		return nil, fmt.Errorf("list tree %s in %s at %s: %w", prefix, repoDir, rev, err)
	}

	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil, nil
	}

	lines := strings.Split(trimmed, "\n")
	relative := make([]string, len(lines))
	for i, line := range lines {
		rel, err := filepath.Rel(prefix, line)
		if err != nil {
			return nil, fmt.Errorf("relativize %s to %s: %w", line, prefix, err)
		}
		relative[i] = rel
	}
	return relative, nil
}
