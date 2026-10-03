package git

import (
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// mergeTreeConflictExit is the exit status `git merge-tree` uses to report
// that the merge has conflicts, as opposed to failing outright.
const mergeTreeConflictExit = 1

// mergeTree runs `git merge-tree --write-tree extra... remoteHead localHead`
// and returns the merged tree with the conflicted paths, sorted. The tree is
// returned on a conflict too: every path outside conflicts is merged
// correctly in it, and callers never take a conflicted path from it.
func mergeTree(dir, remoteHead, localHead string, extra ...string) (string, []string, error) {
	args := append([]string{"merge-tree", "--write-tree"}, extra...)
	args = append(args, remoteHead, localHead)
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err == nil {
		return strings.TrimSpace(string(out)), nil, nil
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return "", nil, fmt.Errorf("merge in memory in %s: %w", dir, err)
	}
	if exitErr.ExitCode() != mergeTreeConflictExit {
		return "", nil, fmt.Errorf("merge in memory in %s: %w: %s", dir, err, strings.TrimSpace(string(exitErr.Stderr)))
	}

	// The first line is the tree; conflict stage lines follow until the
	// first blank line, after which come informational messages.
	lines := strings.Split(string(out), "\n")
	var stageLines []string
	for _, line := range lines[1:] {
		if line == "" {
			break
		}
		stageLines = append(stageLines, line)
	}
	paths, err := parseUnmerged(stageLines)
	if err != nil {
		return "", nil, fmt.Errorf("read conflicts in %s: %w", dir, err)
	}
	return strings.TrimSpace(lines[0]), paths, nil
}

// parseUnmerged returns the distinct paths of `<mode> <oid> <stage>\t<path>`
// lines, sorted.
func parseUnmerged(lines []string) ([]string, error) {
	seen := map[string]bool{}
	var paths []string
	for _, line := range lines {
		meta, path, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		if len(strings.Fields(meta)) != 3 {
			return nil, fmt.Errorf("unexpected conflict line %q", line)
		}
		if !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths, nil
}
