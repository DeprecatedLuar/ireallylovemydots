// Package paths resolves dotz's three XDG directories and creates them on demand.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	appDirName = "ireallylovemydots"

	dataHomeEnv   = "XDG_DATA_HOME"
	configHomeEnv = "XDG_CONFIG_HOME"
	stateHomeEnv  = "XDG_STATE_HOME"

	defaultDataRel   = ".local/share"
	defaultConfigRel = ".config"
	defaultStateRel  = ".local/state"

	dirPerm = 0700
)

// Data returns $XDG_DATA_HOME/dots, creating it if absent.
func Data() (string, error) {
	return resolve(dataHomeEnv, defaultDataRel)
}

// Config returns $XDG_CONFIG_HOME/dots, creating it if absent.
func Config() (string, error) {
	return resolve(configHomeEnv, defaultConfigRel)
}

// State returns $XDG_STATE_HOME/dots, creating it if absent.
func State() (string, error) {
	return resolve(stateHomeEnv, defaultStateRel)
}

func resolve(envVar, defaultRel string) (string, error) {
	base := os.Getenv(envVar)
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		base = filepath.Join(home, defaultRel)
	}
	dir := filepath.Join(base, appDirName)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	return dir, nil
}

// InsideDataDir reports whether path resolves to somewhere inside the data
// directory, following symlinks on every existing path component. Used by
// the in-repo link guard: a destination must never resolve, directly or
// through an intermediate symlink, into the data directory.
func InsideDataDir(path string) (bool, error) {
	dataDir, err := Data()
	if err != nil {
		return false, err
	}
	resolvedData, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		return false, fmt.Errorf("resolve data directory: %w", err)
	}

	resolvedPath, err := resolveExisting(path)
	if err != nil {
		return false, err
	}

	rel, err := filepath.Rel(resolvedData, resolvedPath)
	if err != nil {
		return false, nil
	}
	return rel == "." || (rel != ".." && !hasParentPrefix(rel)), nil
}

func hasParentPrefix(rel string) bool {
	return len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator)
}

// IsProtectedRoot reports whether path is one of the roots dots refuses as a
// destination outright, per concept.md "Destinations dots will not link":
// ~, /, and the XDG config, data, and state roots themselves. A destination
// beneath one of them is ordinary and unrestricted; the roots themselves are
// not, because linking one replaces the directory tree every other entry —
// and dots itself — resolves through. No flag overrides this.
func IsProtectedRoot(path string) (bool, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, fmt.Errorf("resolve absolute path for %s: %w", path, err)
	}
	clean := filepath.Clean(abs)

	if clean == string(filepath.Separator) {
		return true, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return false, fmt.Errorf("resolve home directory: %w", err)
	}
	if clean == filepath.Clean(home) {
		return true, nil
	}

	for _, resolver := range []func() (string, error){Data, Config, State} {
		root, err := resolver()
		if err != nil {
			return false, err
		}
		if clean == filepath.Clean(root) {
			return true, nil
		}
	}
	return false, nil
}

// resolveExisting evaluates symlinks on the longest existing prefix of path,
// then rejoins the remaining (not-yet-created) components verbatim.
func resolveExisting(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path for %s: %w", path, err)
	}

	remainder := ""
	current := abs
	for {
		if _, err := os.Lstat(current); err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", fmt.Errorf("resolve symlinks for %s: %w", current, err)
			}
			return filepath.Join(resolved, remainder), nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return abs, nil
		}
		remainder = filepath.Join(filepath.Base(current), remainder)
		current = parent
	}
}
