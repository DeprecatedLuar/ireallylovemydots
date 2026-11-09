package git

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// settleIndexSuffix names the throwaway index settleTree builds in, beside
// the real one.
const settleIndexSuffix = ".dots-settle"

// removeEntry is the update-index --index-info mode and object id that
// remove a path from the index.
const removeEntry = "0 0000000000000000000000000000000000000000"

// settleTree returns merged with each of paths replaced by side's version:
// every entry at a path, below it, or moved aside beside it is dropped, then
// side's entries for it are added. A path side does not have stays removed.
// The real index is never touched.
func settleTree(dir, merged, side string, paths []string) (string, error) {
	if len(paths) == 0 {
		return merged, nil
	}
	index, err := indexPath(dir)
	if err != nil {
		return "", err
	}
	tmp := index + settleIndexSuffix
	defer os.Remove(tmp)
	env := []string{"GIT_INDEX_FILE=" + tmp}

	if out, err := gitCmdEnv(dir, env, "read-tree", merged); err != nil {
		return "", fmt.Errorf("load merge in %s: %s", dir, strings.TrimSpace(out))
	}
	listed, err := gitCmdEnv(dir, env, "ls-files", "-z", "--cached")
	if err != nil {
		return "", fmt.Errorf("list merge in %s: %s", dir, strings.TrimSpace(listed))
	}
	var info []string
	for _, f := range strings.Split(listed, "\x00") {
		if f != "" && settles(f, paths) {
			info = append(info, removeEntry+"\t"+f)
		}
	}
	for _, p := range paths {
		out, err := gitCmd(dir, "ls-tree", "-r", "-z", side, "--", p)
		if err != nil {
			return "", fmt.Errorf("list %s in %s: %s", p, dir, strings.TrimSpace(out))
		}
		for _, line := range strings.Split(out, "\x00") {
			if line != "" {
				info = append(info, line)
			}
		}
	}

	cmd := exec.Command("git", "update-index", "-z", "--index-info")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader(strings.Join(info, "\x00") + "\x00")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("settle conflicts in %s: %s", dir, strings.TrimSpace(string(out)))
	}
	out, err := gitCmdEnv(dir, env, "write-tree", "--missing-ok")
	if err != nil {
		return "", fmt.Errorf("write settled tree in %s: %s", dir, strings.TrimSpace(out))
	}
	return strings.TrimSpace(out), nil
}

// settles reports whether index path f is replaced when settling paths: it
// is one of them, lies below one, or is one moved aside by merge-tree.
func settles(f string, paths []string) bool {
	for _, p := range paths {
		if f == p || strings.HasPrefix(f, p+"/") || (strings.HasPrefix(f, p+"~") && realPath(f) == p) {
			return true
		}
	}
	return false
}
