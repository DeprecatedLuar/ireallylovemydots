package git

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// emptyTree is git's object id for a tree with no entries.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// snapshotIndexSuffix names the throwaway index snapshotTree stages into,
// beside the real one.
const snapshotIndexSuffix = ".dots-snapshot"

// gitCmdEnv runs a git command in dir with extra environment entries and
// returns its combined output.
func gitCmdEnv(dir string, env []string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// headCommit returns dir's HEAD commit, or "" on a branch with no commits.
func headCommit(dir string) (string, error) {
	out, err := gitCmd(dir, "rev-parse", "--verify", "-q", "HEAD")
	if err == nil {
		return strings.TrimSpace(out), nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && strings.TrimSpace(out) == "" {
		return "", nil
	}
	return "", fmt.Errorf("resolve HEAD in %s: %s", dir, strings.TrimSpace(out))
}

// topLevel returns treeish's top-level entries keyed by name, each value
// the raw `ls-tree` line mkTree accepts back. An empty treeish has none.
func topLevel(dir, treeish string) (map[string]string, error) {
	entries := map[string]string{}
	if treeish == "" {
		return entries, nil
	}
	out, err := gitCmd(dir, "ls-tree", "-z", treeish)
	if err != nil {
		return nil, fmt.Errorf("list %s in %s: %s", treeish, dir, strings.TrimSpace(out))
	}
	for _, line := range strings.Split(out, "\x00") {
		if line == "" {
			continue
		}
		_, name, ok := strings.Cut(line, "\t")
		if !ok {
			return nil, fmt.Errorf("unexpected ls-tree line %q in %s", line, dir)
		}
		entries[name] = line
	}
	return entries, nil
}

// isTreeLine reports whether an ls-tree line names a directory.
func isTreeLine(line string) bool {
	fields := strings.Fields(line)
	return len(fields) >= 2 && fields[1] == "tree"
}

// mkTree writes a tree holding exactly lines, each an ls-tree line.
func mkTree(dir string, lines []string) (string, error) {
	cmd := exec.Command("git", "mktree", "-z")
	cmd.Dir = dir
	input := ""
	if len(lines) > 0 {
		input = strings.Join(lines, "\x00") + "\x00"
	}
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("build tree in %s: %s", dir, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// commitTree writes a commit of tree with parents and returns its id.
func commitTree(dir, tree, msg string, parents ...string) (string, error) {
	args := []string{"commit-tree", tree, "-m", msg}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	out, err := gitCmd(dir, args...)
	if err != nil {
		return "", fmt.Errorf("commit tree in %s: %s", dir, strings.TrimSpace(out))
	}
	return strings.TrimSpace(out), nil
}

// treeOf returns commit's tree, or the empty tree for "".
func treeOf(dir, commit string) (string, error) {
	if commit == "" {
		return emptyTree, nil
	}
	return resolveRef(dir, commit+"^{tree}")
}

// snapshotTree stages the whole working tree into a copy of dir's index and
// writes it as a tree. Copying the real index keeps uninstalled namespaces'
// skip-worktree entries, so they are carried, not deleted; the real index
// is never touched.
func snapshotTree(dir string) (string, error) {
	out, err := gitCmd(dir, "rev-parse", "--git-path", "index")
	if err != nil {
		return "", fmt.Errorf("locate index in %s: %s", dir, strings.TrimSpace(out))
	}
	index := strings.TrimSpace(out)
	if !filepath.IsAbs(index) {
		index = filepath.Join(dir, index)
	}
	tmp := index + snapshotIndexSuffix
	defer os.Remove(tmp)
	if err := copyFile(index, tmp); err != nil {
		return "", err
	}

	env := []string{"GIT_INDEX_FILE=" + tmp}
	if out, err := gitCmdEnv(dir, env, "add", "-A"); err != nil {
		return "", fmt.Errorf("snapshot changes in %s: %s", dir, strings.TrimSpace(out))
	}
	out, err = gitCmdEnv(dir, env, "write-tree")
	if err != nil {
		return "", fmt.Errorf("snapshot tree in %s: %s", dir, strings.TrimSpace(out))
	}
	return strings.TrimSpace(out), nil
}

// copyFile copies src to dst. A missing src (no index yet) leaves dst
// absent, which git reads as an empty index.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("copy %s: %w", src, err)
	}
	return out.Close()
}
