package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/manifest"
)

func TestDeriveNameOwner(t *testing.T) {
	cases := []struct {
		url       string
		wantName  string
		wantOwner string
	}{
		{"https://github.com/DeprecatedLuar/dotfiles.git", "dotfiles", "DeprecatedLuar"},
		{"https://github.com/DeprecatedLuar/dotfiles", "dotfiles", "DeprecatedLuar"},
		{"git@github.com:DeprecatedLuar/dotfiles.git", "dotfiles", "DeprecatedLuar"},
		{"https://example.com/dotfiles.git", "dotfiles", "example.com"},
		{"DeprecatedLuar/dotfiles", "dotfiles", "DeprecatedLuar"},
	}
	for _, c := range cases {
		name, owner := DeriveNameOwner(c.url)
		if name != c.wantName || owner != c.wantOwner {
			t.Errorf("DeriveNameOwner(%q) = (%q, %q), want (%q, %q)", c.url, name, owner, c.wantName, c.wantOwner)
		}
	}
}

func TestCandidateURLs(t *testing.T) {
	cases := []struct {
		spec string
		want []string
	}{
		{"https://github.com/owner/repo.git", []string{"https://github.com/owner/repo.git"}},
		{"git@github.com:owner/repo.git", []string{"git@github.com:owner/repo.git"}},
		{"github.com/owner/repo", []string{"https://github.com/owner/repo"}},
		{"gitlab.com/owner/repo", []string{"https://gitlab.com/owner/repo"}},
		{"owner/repo", []string{
			"https://github.com/owner/repo",
			"https://gitlab.com/owner/repo",
		}},
	}
	for _, c := range cases {
		got := candidateURLs(c.spec)
		if len(got) != len(c.want) {
			t.Errorf("candidateURLs(%q) = %v, want %v", c.spec, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("candidateURLs(%q) = %v, want %v", c.spec, got, c.want)
				break
			}
		}
	}
}

func TestLooksLikeNotFound(t *testing.T) {
	if !looksLikeNotFound("fatal: repository 'https://github.com/x/y' not found") {
		t.Error("expected GitHub-style 'not found' to match")
	}
	if !looksLikeNotFound("remote: The project you were looking for could not be found") {
		t.Error("expected GitLab-style 'could not be found' to match")
	}
	if looksLikeNotFound("fatal: could not read Username for 'https://github.com': terminal prompts disabled") {
		t.Error("auth failure should not be treated as not-found")
	}
}

func TestResolve_BareNameAndOwnerName(t *testing.T) {
	repos := []manifest.Repo{
		{Name: "dotfiles", Owner: "DeprecatedLuar", URL: "https://example.com/DeprecatedLuar/dotfiles"},
	}

	if _, err := Resolve(repos, "dotfiles"); err != nil {
		t.Fatalf("Resolve by bare name: %v", err)
	}
	if _, err := Resolve(repos, "DOTFILES"); err != nil {
		t.Fatalf("Resolve by bare name, case-insensitive: %v", err)
	}
	if _, err := Resolve(repos, "deprecatedluar/dotfiles"); err != nil {
		t.Fatalf("Resolve by owner/name: %v", err)
	}
	if _, err := Resolve(repos, "someone-else/dotfiles"); err == nil {
		t.Fatalf("Resolve matched the wrong owner")
	}
}

// newSourceRepo builds a small git repository with one commit containing a
// namespace-shaped directory, standing in for a real dotfiles remote.
func newSourceRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")

	if err := os.MkdirAll(filepath.Join(dir, "editors"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "editors", ".dots"), []byte("[]"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")
	return dir
}

func TestClone_BloblessNoCheckout(t *testing.T) {
	source := newSourceRepo(t)
	dataDir := t.TempDir()

	dest, resolvedURL, err := Clone(dataDir, "file://"+source, "dotfiles")
	if err != nil {
		t.Fatalf("Clone error: %v", err)
	}
	if resolvedURL != "file://"+source {
		t.Fatalf("resolvedURL = %q, want %q", resolvedURL, "file://"+source)
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); err != nil {
		t.Fatalf("clone missing .git: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "editors")); !os.IsNotExist(err) {
		t.Fatalf("expected no-checkout clone to leave the worktree empty, got err=%v", err)
	}

	names, err := Namespaces(dest)
	if err != nil {
		t.Fatalf("Namespaces error: %v", err)
	}
	if len(names) != 1 || names[0] != "editors" {
		t.Fatalf("Namespaces = %v, want [editors]", names)
	}
}

func TestClone_DestinationAlreadyExists(t *testing.T) {
	source := newSourceRepo(t)
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "dotfiles"), 0755); err != nil {
		t.Fatal(err)
	}

	if _, _, err := Clone(dataDir, "file://"+source, "dotfiles"); err == nil {
		t.Fatal("expected error cloning into an existing destination")
	}
}

func TestClone_FailedCloneLeavesNoPartialDirectory(t *testing.T) {
	dataDir := t.TempDir()

	if _, _, err := Clone(dataDir, "file:///nonexistent/repo", "dotfiles"); err == nil {
		t.Fatal("expected clone of a bad URL to fail")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "dotfiles")); !os.IsNotExist(err) {
		t.Fatalf("expected no partial clone directory, got err=%v", err)
	}
}
