package git

import (
	"fmt"
	"os/exec"
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
