package repo

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// dotsFileName mirrors manifest.Path's file name. It is not imported from
// the manifest package to avoid a dependency cycle risk between repo and
// manifest; the sentinel name is part of the on-disk contract, not an
// implementation detail either package owns exclusively.
const dotsFileName = ".dots"

// RootEntry is one top-level entry of a repository or a plain folder: its
// name, whether it is a directory, and whether it directly holds a .dots
// manifest — which is what makes a top-level folder a namespace, per
// concept.md "Namespace".
type RootEntry struct {
	Name    string
	IsDir   bool
	HasDots bool
}

// RootEntries reads a cloned repository's root entries via one
// `git ls-tree -r --full-tree --name-only HEAD`. This touches only tree
// objects, so it works against a blobless, no-checkout clone without
// materializing anything. An empty repository (no commits yet) has no root
// entries. --full-tree keeps the listing relative to the repository root
// rather than to repoPath's position inside it, so a path below the root
// can never read as an empty repository. Everything that needs a
// repository's top-level shape reads this rather than shelling out to git
// again.
func RootEntries(repoPath string) ([]RootEntry, error) {
	cmd := exec.Command("git", "-C", repoPath, "ls-tree", "-r", "--full-tree", "--name-only", "HEAD")
	out, err := cmd.CombinedOutput()
	if err != nil {
		if looksLikeNoCommits(string(out)) {
			return nil, nil
		}
		return nil, fmt.Errorf("list root entries in %s: %s", repoPath, strings.TrimSpace(string(out)))
	}
	return parseRootEntries(strings.TrimSpace(string(out))), nil
}

// looksLikeNoCommits reports whether git's ls-tree failure indicates HEAD
// does not exist yet — an uncommitted repository, which reads as empty —
// as opposed to a genuine error. Message wording varies across git
// versions ("unknown revision", "bad revision", "Not a valid object name").
func looksLikeNoCommits(gitOutput string) bool {
	lower := strings.ToLower(gitOutput)
	for _, phrase := range []string{"unknown revision", "bad revision", "not a valid object name"} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func parseRootEntries(lsTreeOutput string) []RootEntry {
	if lsTreeOutput == "" {
		return nil
	}

	index := map[string]int{}
	var entries []RootEntry
	for line := range strings.SplitSeq(lsTreeOutput, "\n") {
		if line == "" {
			continue
		}
		root, rest, isNested := strings.Cut(line, "/")

		i, ok := index[root]
		if !ok {
			i = len(entries)
			index[root] = i
			entries = append(entries, RootEntry{Name: root})
		}
		if isNested {
			entries[i].IsDir = true
			if rest == dotsFileName {
				entries[i].HasDots = true
			}
		}
	}
	return entries
}

// DiskEntries is RootEntries' filesystem sibling: it reads a plain
// directory's top-level entries directly, for a folder that is not a git
// repository yet (repo init's plain-folder case). A directory with no
// entries reads as empty, matching RootEntries on an empty repository.
func DiskEntries(path string) ([]RootEntry, error) {
	dirEntries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("read directory %s: %w", path, err)
	}

	entries := make([]RootEntry, 0, len(dirEntries))
	for _, e := range dirEntries {
		entry := RootEntry{Name: e.Name(), IsDir: e.IsDir()}
		if entry.IsDir {
			if _, err := os.Stat(filepath.Join(path, e.Name(), dotsFileName)); err == nil {
				entry.HasDots = true
			}
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// Namespaces reads the namespace catalogue from a cloned repository at
// repoPath: the root entries that hold a .dots manifest, which is what
// makes a top-level folder a namespace rather than an arbitrary source
// folder — see concept.md "Namespace".
func Namespaces(repoPath string) ([]string, error) {
	entries, err := RootEntries(repoPath)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.HasDots {
			names = append(names, e.Name)
		}
	}
	return names, nil
}
