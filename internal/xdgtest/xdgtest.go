// Package xdgtest confines a test binary's XDG directories to a throwaway
// sandbox.
//
// Every dots directory is resolved from an environment variable, so a test
// that forgets to override one resolves it against the real machine — and
// since manifest.WriteRegistry writes both registry files on every call,
// a test that overrides only XDG_CONFIG_HOME still truncates the developer's
// own $XDG_STATE_HOME/local-repositories.toml. Pointing all three at a
// sandbox before any test runs makes that impossible to reach: a test may
// still narrow a variable to its own t.TempDir(), but the ones it does not
// name resolve inside the sandbox rather than at home.
package xdgtest

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

const (
	sandboxPrefix = "dots-xdgtest"
	sandboxPerm   = 0700

	// git resolves its global config out of $XDG_CONFIG_HOME/git/config, so
	// sandboxing that variable also hides the developer's identity from
	// every test that shells out to git. Rather than leak the real config
	// back in, the sandbox supplies its own: the identity a commit made by
	// a test is authored with is a detail of the test, not of whoever runs
	// it.
	gitConfigEnv  = "GIT_CONFIG_GLOBAL"
	gitConfigName = "gitconfig"
	gitConfigBody = "[user]\n\tname = dots tests\n\temail = tests@dots.invalid\n"
	gitConfigPerm = 0600
)

// vars is every environment variable internal/paths resolves a directory
// from. Adding a fourth directory there means adding it here too.
var vars = []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"}

// Main runs m with all three XDG variables pointed at a fresh sandbox, and
// removes the sandbox afterwards. Every test package in this module wires
// its TestMain to it:
//
//	func TestMain(m *testing.M) { os.Exit(xdgtest.Main(m)) }
//
// Returning the code rather than exiting keeps the sandbox cleanup on a
// path that actually runs.
func Main(m *testing.M) int {
	root, err := os.MkdirTemp("", sandboxPrefix)
	if err != nil {
		fmt.Fprintf(os.Stderr, "xdgtest: create sandbox: %v\n", err)
		return 1
	}
	defer os.RemoveAll(root)

	for _, name := range vars {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, sandboxPerm); err != nil {
			fmt.Fprintf(os.Stderr, "xdgtest: create %s: %v\n", dir, err)
			return 1
		}
		if err := os.Setenv(name, dir); err != nil {
			fmt.Fprintf(os.Stderr, "xdgtest: set %s: %v\n", name, err)
			return 1
		}
	}

	gitConfig := filepath.Join(root, gitConfigName)
	if err := os.WriteFile(gitConfig, []byte(gitConfigBody), gitConfigPerm); err != nil {
		fmt.Fprintf(os.Stderr, "xdgtest: write %s: %v\n", gitConfig, err)
		return 1
	}
	if err := os.Setenv(gitConfigEnv, gitConfig); err != nil {
		fmt.Fprintf(os.Stderr, "xdgtest: set %s: %v\n", gitConfigEnv, err)
		return 1
	}

	return m.Run()
}
