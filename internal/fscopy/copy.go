// Package fscopy copies a single filesystem entry — a file, a directory, or
// a symlink — from one path to another. Used by dots cp (and, per
// implementation-plan.md's Phase 18, restore) wherever a payload already
// materialized on disk needs to end up somewhere else, verbatim.
package fscopy

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// dirPerm is used for the parent directories Copy creates on dst's behalf;
// a copied directory itself always takes its source's own mode instead (see
// copyDir).
const dirPerm = 0755

// Copy copies src to dst: a regular file with its mode, a directory
// recursively, a symlink as a symlink with its link text unchanged. dst must
// not exist. Parent directories of dst are created.
func Copy(src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("copy destination %s already exists", dst)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", dst, err)
	}

	info, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("stat %s: %w", src, err)
	}

	if err := os.MkdirAll(filepath.Dir(dst), dirPerm); err != nil {
		return fmt.Errorf("create parent directory for %s: %w", dst, err)
	}

	switch {
	case info.Mode()&os.ModeSymlink != 0:
		return copySymlink(src, dst)
	case info.IsDir():
		return copyDir(src, dst, info.Mode())
	default:
		return copyFile(src, dst, info.Mode())
	}
}

// copySymlink recreates src's link text at dst, unresolved — the symlink
// itself is copied, never its target's content.
func copySymlink(src, dst string) error {
	target, err := os.Readlink(src)
	if err != nil {
		return fmt.Errorf("read link %s: %w", src, err)
	}
	if err := os.Symlink(target, dst); err != nil {
		return fmt.Errorf("create symlink %s: %w", dst, err)
	}
	return nil
}

// copyFile copies a regular file's bytes, creating dst with src's
// permission bits.
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy %s to %s: %w", src, dst, err)
	}
	return nil
}

// copyDir recreates src's directory tree at dst, recursively, via Copy for
// every entry — so a nested file, directory, or symlink is handled the same
// way whether it sits at src's top level or several levels down.
func copyDir(src, dst string, mode os.FileMode) error {
	if err := os.Mkdir(dst, mode.Perm()); err != nil {
		return fmt.Errorf("create directory %s: %w", dst, err)
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("read directory %s: %w", src, err)
	}
	for _, e := range entries {
		if err := Copy(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return nil
}
