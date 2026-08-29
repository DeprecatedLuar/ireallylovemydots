package repo

import (
	"fmt"
	"os/exec"
	"strings"
)

// Namespaces reads the namespace catalogue from a cloned repository at
// repoPath: the top-level directory names in HEAD's tree, via
// `git ls-tree`. This touches only tree objects, so it works on a blobless,
// no-checkout clone without materializing anything. An empty repository
// (no commits yet) has no catalogue.
func Namespaces(repoPath string) ([]string, error) {
	cmd := exec.Command("git", "-C", repoPath, "ls-tree", "-d", "--name-only", "HEAD")
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "unknown revision") || strings.Contains(string(out), "bad revision") {
			return nil, nil
		}
		return nil, fmt.Errorf("list namespaces in %s: %s", repoPath, strings.TrimSpace(string(out)))
	}

	var names []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if line == "" || strings.HasPrefix(line, ".") {
			continue
		}
		names = append(names, line)
	}
	return names, nil
}
