package repo

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Take moves the folder at srcPath into dataDir under name, so repo init's
// result is owned by dots exactly like Clone's destination — required
// because enable materializes a namespace by sparse checkout, and the
// in-repo link guard is defined against the data directory. os.Rename is
// tried first; on a cross-device error it falls back to a full copy
// followed by removing the source, so nothing is left behind either way.
// The destination must not already exist. Returns the new path.
func Take(dataDir, srcPath, name string) (string, error) {
	dest := filepath.Join(dataDir, name)
	if _, err := os.Stat(dest); err == nil {
		return "", fmt.Errorf("take destination %s already exists", dest)
	}

	renameErr := os.Rename(srcPath, dest)
	if renameErr == nil {
		return dest, nil
	}
	if !errors.Is(renameErr, syscall.EXDEV) {
		return "", fmt.Errorf("move %s to %s: %w", srcPath, dest, renameErr)
	}

	if err := copyTree(srcPath, dest); err != nil {
		os.RemoveAll(dest)
		return "", fmt.Errorf("copy %s to %s: %w", srcPath, dest, err)
	}
	if err := os.RemoveAll(srcPath); err != nil {
		return "", fmt.Errorf("remove source %s after cross-filesystem copy: %w", srcPath, err)
	}
	return dest, nil
}

// copyTree recursively copies src to dst, preserving directories, regular
// files' contents and permissions, and symlinks verbatim. It is Take's
// fallback when os.Rename cannot move across filesystems.
func copyTree(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}

	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	case info.IsDir():
		if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	default:
		return copyFile(src, dst, info.Mode().Perm())
	}
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// IsGitRepo reports whether path is already a git repository, via
// `git rev-parse --show-toplevel`, which is only accepted as a yes when the
// toplevel git reports *is* path itself. git walks upwards, so a plain
// folder nested inside an unrelated repository (say a scratch folder in a
// project checkout) otherwise answers yes and gets read as that outer
// repository — registering as empty and skipping both the compatibility
// check and `git init`. Shared by EnsureGit and repo init's compatibility
// check, which needs to know before EnsureGit runs whether to read the
// folder via RootEntries or DiskEntries.
func IsGitRepo(path string) (bool, error) {
	cmd := exec.Command("git", "-C", path, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return false, nil
		}
		return false, fmt.Errorf("check git repository at %s: %w", path, err)
	}

	toplevel, err := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
	if err != nil {
		return false, fmt.Errorf("resolve git toplevel for %s: %w", path, err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false, fmt.Errorf("resolve %s: %w", path, err)
	}
	return toplevel == resolved, nil
}

// EnsureGit runs `git init` at path only if it is not already a git
// repository, per concept.md "Initializing a local folder": an existing
// repository is taken as it is, with its remote and history intact. Never
// commits, never stages.
func EnsureGit(path string) error {
	isRepo, err := IsGitRepo(path)
	if err != nil {
		return err
	}
	if isRepo {
		return nil
	}

	cmd := exec.Command("git", "init", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git init %s: %s", path, strings.TrimSpace(string(out)))
	}
	return nil
}
