package xdgtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	moduleRootRel = "../.."
	wiring        = "xdgtest.Main(m)"
)

// TestEveryTestPackageIsSandboxed fails when a package carrying tests does
// not wire its TestMain to Main. Without that wiring the package's tests
// resolve whichever XDG directory they forgot to override against the real
// machine — the way `go test ./...` silently truncated a developer's
// local-repositories.toml. A new test package must opt in; this is what
// makes that opt-in non-optional.
func TestEveryTestPackageIsSandboxed(t *testing.T) {
	selfDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}

	var unsandboxed []string
	err = filepath.WalkDir(moduleRootRel, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		if abs == selfDir || strings.HasPrefix(d.Name(), ".") && path != moduleRootRel {
			return filepath.SkipDir
		}

		sandboxed, hasTests, err := inspectPackage(path)
		if err != nil {
			return err
		}
		if hasTests && !sandboxed {
			unsandboxed = append(unsandboxed, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk module: %v", err)
	}

	if len(unsandboxed) > 0 {
		t.Fatalf("test packages with no XDG sandbox: %v\n"+
			"add a file declaring: func TestMain(m *testing.M) { os.Exit(xdgtest.Main(m)) }",
			unsandboxed)
	}
}

// inspectPackage reports whether dir holds test files at all, and whether
// any of them wires TestMain to Main.
func inspectPackage(dir string) (sandboxed, hasTests bool, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, false, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		hasTests = true
		content, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return false, false, err
		}
		if strings.Contains(string(content), wiring) {
			sandboxed = true
		}
	}
	return sandboxed, hasTests, nil
}

// TestMain_SandboxesAllThreeVariables pins the guarantee the wiring above
// exists for: inside a sandboxed binary, no XDG variable points at the real
// machine's directories.
func TestMain_SandboxesAllThreeVariables(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range vars {
		value := os.Getenv(name)
		if value == "" {
			t.Fatalf("%s is unset inside the sandbox", name)
		}
		if rel, err := filepath.Rel(home, value); err == nil && !strings.HasPrefix(rel, "..") {
			t.Fatalf("%s=%s resolves inside the real home directory", name, value)
		}
	}
}

// This package's own tests run inside the sandbox too, calling Main
// directly since the wiring the guard looks for is spelled with a package
// qualifier everywhere else.
func TestMain(m *testing.M) { os.Exit(Main(m)) }
