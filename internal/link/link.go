// Package link holds symlink primitives only: create, remove, read, and
// classify. No orchestration — callers decide what a classification means.
package link

import (
	"fmt"
	"os"
)

// State classifies what currently exists at a destination path.
type State int

const (
	// Missing means nothing exists at the path.
	Missing State = iota
	// CorrectSymlink means a symlink exists and points at the expected target.
	CorrectSymlink
	// WrongSymlink means a symlink exists but points somewhere else.
	WrongSymlink
	// RealFile means a regular file exists, not a symlink.
	RealFile
	// RealDir means a directory exists, not a symlink.
	RealDir
)

// Classify reports what exists at path relative to the expected symlink
// target. wantTarget is ignored when nothing or a non-symlink occupies path.
func Classify(path, wantTarget string) (State, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return Missing, nil
	}
	if err != nil {
		return Missing, fmt.Errorf("lstat %s: %w", path, err)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return Missing, fmt.Errorf("readlink %s: %w", path, err)
		}
		if target == wantTarget {
			return CorrectSymlink, nil
		}
		return WrongSymlink, nil
	}

	if info.IsDir() {
		return RealDir, nil
	}
	return RealFile, nil
}

// Create makes a symlink at path pointing to target. The parent directory
// must already exist; Create does not create it.
func Create(path, target string) error {
	if err := os.Symlink(target, path); err != nil {
		return fmt.Errorf("create symlink %s -> %s: %w", path, target, err)
	}
	return nil
}

// Remove deletes the symlink at path. It is an error if nothing exists
// there; callers that tolerate absence should check with Classify first.
func Remove(path string) error {
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove symlink %s: %w", path, err)
	}
	return nil
}

// Read returns the target a symlink at path points to.
func Read(path string) (string, error) {
	target, err := os.Readlink(path)
	if err != nil {
		return "", fmt.Errorf("readlink %s: %w", path, err)
	}
	return target, nil
}

// ReadIfSymlink reports whether path is currently a symlink and, if so, what
// it points to. isSymlink is false whenever nothing exists at path or
// whatever is there is not a symlink — both read simply as "nothing to go
// on" rather than an error, since a caller proving ownership of a
// destination (self-heal's stranded-link cleanup, namespace manifest
// recovery) treats a missing or non-symlink path the same way: no evidence.
// A symlink present but unreadable is the one case reported as an error, so
// a caller that must act on every recorded destination (cleanup) can
// surface it rather than silently skip.
func ReadIfSymlink(path string) (target string, isSymlink bool, err error) {
	info, statErr := os.Lstat(path)
	if statErr != nil {
		return "", false, nil
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return "", false, nil
	}
	target, err = os.Readlink(path)
	if err != nil {
		return "", true, fmt.Errorf("readlink %s: %w", path, err)
	}
	return target, true, nil
}
