// Package git holds the one shared fact-finding primitive removal reads
// before it acts: the git state of a repository clone. It reports facts
// only — no policy, no prompting. Callers (repo rm, namespace rm, uninstall)
// decide what to do with them, per concept.md "Git safety on removal".
package git

import (
	"fmt"
	"os/exec"
	"strings"
)

// RepoStatus is the git state of a repository clone relevant to removal
// safety.
type RepoStatus struct {
	// Dirty lists paths with uncommitted changes, per `git status --porcelain`.
	Dirty []string
	// Unpushed is the count of local commits not present on the upstream
	// remote-tracking branch. Zero when there is no upstream to compare
	// against, which IsGitRepo callers are expected to treat via HasRemote.
	Unpushed int
	// HasRemote reports whether the repository has any remote configured.
	// A directory that is not a git repository at all reads as true here —
	// deliberately: this check exists to protect real clones, and a bare
	// non-repository folder is a different failure mode entirely, outside
	// what this package is asked about.
	HasRemote bool
}

// Status reads repoDir's git state: uncommitted paths, the count of commits
// ahead of its upstream, and whether it has a remote at all. A directory
// that is not a git repository reads as a clean, remote-backed repository —
// this check exists to protect real clones from a destructive removal, and
// has nothing to say about a directory that was never a git repository in
// the first place.
func Status(repoDir string) (RepoStatus, error) {
	if !isGitRepo(repoDir) {
		return RepoStatus{HasRemote: true}, nil
	}

	dirty, err := dirtyPaths(repoDir)
	if err != nil {
		return RepoStatus{}, err
	}
	hasRemote, err := hasRemote(repoDir)
	if err != nil {
		return RepoStatus{}, err
	}
	unpushed := 0
	if hasRemote {
		unpushed, err = unpushedCount(repoDir)
		if err != nil {
			return RepoStatus{}, err
		}
	}

	return RepoStatus{Dirty: dirty, Unpushed: unpushed, HasRemote: hasRemote}, nil
}

func isGitRepo(repoDir string) bool {
	cmd := exec.Command("git", "-C", repoDir, "rev-parse", "--git-dir")
	return cmd.Run() == nil
}

// dirtyPaths shells out to `git status --porcelain -z` rather than the
// line-based `--porcelain` format: in `-z` mode fields are NUL-separated and
// paths are never C-quoted, so a filename with non-ASCII bytes (e.g.
// "café.kpp") comes back literal instead of as `"caf\303\251.kpp"` — which
// the line-based parser would split on the stray leading quote. A rename or
// copy record carries two NUL-terminated fields — the destination path,
// then the source path — instead of one, per `git help status`'s "-z"
// section; both are reported dirty, since both namespaces hold uncommitted
// work.
func dirtyPaths(repoDir string) ([]string, error) {
	cmd := exec.Command("git", "-C", repoDir, "status", "--porcelain", "-z")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git status %s: %w", repoDir, err)
	}
	fields := strings.Split(string(out), "\x00")
	var paths []string
	for i := 0; i < len(fields); i++ {
		entry := fields[i]
		if entry == "" {
			continue
		}
		// Porcelain format: two status letters, a space, then the path.
		if len(entry) <= 3 {
			continue
		}
		x, y := entry[0], entry[1]
		paths = append(paths, entry[3:])
		if x == 'R' || x == 'C' || y == 'R' || y == 'C' {
			// The source path is a second NUL-terminated field right after
			// this one.
			i++
			if i < len(fields) && fields[i] != "" {
				paths = append(paths, fields[i])
			}
		}
	}
	return paths, nil
}

func hasRemote(repoDir string) (bool, error) {
	cmd := exec.Command("git", "-C", repoDir, "remote")
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("git remote %s: %w", repoDir, err)
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// RemoteURL returns repoDir's "origin" remote URL, or "" if it has none —
// `repo adopt`'s way of recovering what a clone was cloned from when its
// registry entry is gone but the clone itself is not.
func RemoteURL(repoDir string) (string, error) {
	cmd := exec.Command("git", "-C", repoDir, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(string(out)), nil
}

// unpushedCount returns the number of commits on HEAD not reachable from its
// upstream remote-tracking branch. No upstream configured (a remote exists
// but the current branch is not tracking it) reads as zero rather than an
// error — there is nothing to compare against, and hasRemote already covers
// the "no remote at all" case this check exists for.
func unpushedCount(repoDir string) (int, error) {
	cmd := exec.Command("git", "-C", repoDir, "rev-list", "--count", "@{u}..HEAD")
	out, err := cmd.Output()
	if err != nil {
		return 0, nil
	}
	var n int
	if _, scanErr := fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &n); scanErr != nil {
		return 0, nil
	}
	return n, nil
}
