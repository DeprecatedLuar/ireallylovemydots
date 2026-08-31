package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
}

func initRepoWithCommit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "README")
	runGit(t, dir, "commit", "-m", "init")
	return dir
}

func TestStatus_NonGitDirectory_ReadsSafeWithRemote(t *testing.T) {
	dir := t.TempDir()
	st, err := Status(dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(st.Dirty) != 0 || st.Unpushed != 0 || !st.HasRemote {
		t.Fatalf("expected a non-git directory to read as clean/remote-backed, got %+v", st)
	}
}

func TestStatus_CleanRepoNoRemote_ReportsNoRemote(t *testing.T) {
	dir := initRepoWithCommit(t)
	st, err := Status(dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.HasRemote {
		t.Fatal("expected HasRemote false for a repository with no remote configured")
	}
	if len(st.Dirty) != 0 {
		t.Fatalf("expected no dirty paths, got %v", st.Dirty)
	}
}

func TestStatus_DirtyRepo_ReportsDirtyPath(t *testing.T) {
	dir := initRepoWithCommit(t)
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	st, err := Status(dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(st.Dirty) != 1 || st.Dirty[0] != "README" {
		t.Fatalf("expected README reported dirty, got %v", st.Dirty)
	}
}

func TestStatus_UnpushedCommits_ReportsCount(t *testing.T) {
	remote := t.TempDir()
	runGit(t, remote, "init", "--bare")

	dir := initRepoWithCommit(t)
	runGit(t, dir, "remote", "add", "origin", remote)
	runGit(t, dir, "push", "-u", "origin", "HEAD:refs/heads/main")

	if err := os.WriteFile(filepath.Join(dir, "second"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "second")
	runGit(t, dir, "commit", "-m", "second commit")

	st, err := Status(dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.HasRemote {
		t.Fatal("expected HasRemote true")
	}
	if st.Unpushed != 1 {
		t.Fatalf("expected 1 unpushed commit, got %d", st.Unpushed)
	}
}

func TestStatus_PushedRepo_CleanNoUnpushed(t *testing.T) {
	remote := t.TempDir()
	runGit(t, remote, "init", "--bare")

	dir := initRepoWithCommit(t)
	runGit(t, dir, "remote", "add", "origin", remote)
	runGit(t, dir, "push", "-u", "origin", "HEAD:refs/heads/main")

	st, err := Status(dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.HasRemote || st.Unpushed != 0 || len(st.Dirty) != 0 {
		t.Fatalf("expected a clean, fully-pushed repository, got %+v", st)
	}
}
