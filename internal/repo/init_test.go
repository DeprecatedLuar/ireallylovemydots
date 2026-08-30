package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTake_MovesFolderAndLeavesNothingBehind(t *testing.T) {
	dataDir := t.TempDir()
	src := t.TempDir()
	src = filepath.Join(src, "dotfiles")
	if err := os.MkdirAll(filepath.Join(src, "nvim"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "nvim", "init.lua"), []byte("-- config"), 0644); err != nil {
		t.Fatal(err)
	}

	dest, err := Take(dataDir, src, "dotfiles")
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	if dest != filepath.Join(dataDir, "dotfiles") {
		t.Fatalf("dest = %q, want %q", dest, filepath.Join(dataDir, "dotfiles"))
	}
	if _, err := os.Stat(filepath.Join(dest, "nvim", "init.lua")); err != nil {
		t.Fatalf("expected content at new location: %v", err)
	}
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Fatalf("expected nothing left at source (no directory, no symlink), got err=%v", err)
	}
}

func TestTake_DestinationAlreadyExists(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "dotfiles"), 0755); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()

	if _, err := Take(dataDir, src, "dotfiles"); err == nil {
		t.Fatal("expected error taking into an existing destination")
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("expected source untouched: %v", err)
	}
}

func TestCopyTree_PreservesFilesDirsAndSymlinks(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "file.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file.txt", filepath.Join(src, "sub", "link")); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "copy")
	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dst, "sub", "file.txt"))
	if err != nil || string(data) != "hello" {
		t.Fatalf("copied file mismatch: data=%q err=%v", data, err)
	}
	target, err := os.Readlink(filepath.Join(dst, "sub", "link"))
	if err != nil || target != "file.txt" {
		t.Fatalf("copied symlink mismatch: target=%q err=%v", target, err)
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestIsGitRepo(t *testing.T) {
	plain := t.TempDir()
	isRepo, err := IsGitRepo(plain)
	if err != nil {
		t.Fatalf("IsGitRepo: %v", err)
	}
	if isRepo {
		t.Fatal("expected a plain folder to not be a git repository")
	}

	repoDir := t.TempDir()
	gitRun(t, repoDir, "init", "-b", "main")
	isRepo, err = IsGitRepo(repoDir)
	if err != nil {
		t.Fatalf("IsGitRepo: %v", err)
	}
	if !isRepo {
		t.Fatal("expected a git-initialized folder to be recognized as a repository")
	}
}

// TestIsGitRepo_PlainFolderNestedInARepository covers the case git's own
// upward walk gets wrong: a plain folder inside an unrelated repository is
// not itself a repository. Answering yes there made repo init read the
// enclosing repository's tree instead of the folder, which lists nothing for
// an untracked path — so the folder registered as an empty repository,
// skipping both the compatibility check and git init.
func TestIsGitRepo_PlainFolderNestedInARepository(t *testing.T) {
	outer := t.TempDir()
	gitRun(t, outer, "init", "-b", "main")

	nested := filepath.Join(outer, "scratch")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	isRepo, err := IsGitRepo(nested)
	if err != nil {
		t.Fatalf("IsGitRepo: %v", err)
	}
	if isRepo {
		t.Fatal("expected a plain folder nested in a repository to not be a repository itself")
	}
}

func TestEnsureGit_InitsOnlyWhenNotAlreadyARepository(t *testing.T) {
	plain := t.TempDir()
	if err := EnsureGit(plain); err != nil {
		t.Fatalf("EnsureGit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(plain, ".git")); err != nil {
		t.Fatalf("expected .git created: %v", err)
	}

	log, err := exec.Command("git", "-C", plain, "log").CombinedOutput()
	if err == nil {
		t.Fatalf("expected `git log` to fail with no commits, got: %s", log)
	}
	status, err := exec.Command("git", "-C", plain, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if strings.TrimSpace(string(status)) != "" {
		t.Fatalf("expected no content to be staged, git status should be empty in an untouched folder, got: %s", status)
	}
}

func TestEnsureGit_PreservesExistingRepositoryAndRemote(t *testing.T) {
	repoDir := t.TempDir()
	gitRun(t, repoDir, "init", "-b", "main")
	gitRun(t, repoDir, "remote", "add", "origin", "file:///nonexistent/upstream")
	gitRun(t, repoDir, "config", "user.email", "test@example.com")
	gitRun(t, repoDir, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(repoDir, "a.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repoDir, "add", ".")
	gitRun(t, repoDir, "commit", "-m", "init")

	before, err := exec.Command("git", "-C", repoDir, "remote", "-v").CombinedOutput()
	if err != nil {
		t.Fatalf("git remote -v: %v", err)
	}

	if err := EnsureGit(repoDir); err != nil {
		t.Fatalf("EnsureGit: %v", err)
	}

	after, err := exec.Command("git", "-C", repoDir, "remote", "-v").CombinedOutput()
	if err != nil {
		t.Fatalf("git remote -v: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("expected remote untouched: before=%q after=%q", before, after)
	}

	log, err := exec.Command("git", "-C", repoDir, "log", "--oneline").CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	if strings.Count(strings.TrimSpace(string(log)), "\n")+1 != 1 {
		t.Fatalf("expected exactly one preexisting commit, EnsureGit must not commit, got: %s", log)
	}
}
