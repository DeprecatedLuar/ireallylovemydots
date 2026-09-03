package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/paths"
	"github.com/DeprecatedLuar/dotz/internal/repo"
	"github.com/DeprecatedLuar/dotz/internal/state"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// newCatalogueSourceRepo builds a small local git repository holding two
// namespaces, each with one tracked entry pointing at an absolute
// destination under home, standing in for a real dotfiles remote a fresh
// `repo add` would clone but never materialize.
func newCatalogueSourceRepo(t *testing.T, home string) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")

	for _, ns := range []string{"editors", "shell"} {
		nsDir := filepath.Join(dir, ns)
		if err := os.MkdirAll(filepath.Join(nsDir, ns), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(nsDir, ns, "seed"), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
		m := manifest.Manifest{Entries: []manifest.Entry{{Name: ns, Dest: filepath.Join(home, "."+ns)}}}
		data, err := manifest.Encode(m)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(nsDir, ".dots"), data, 0644); err != nil {
			t.Fatal(err)
		}
	}

	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "init")
	return dir
}

// registerClonedCatalogue clones source into a fresh sandbox (its own
// XDG_* directories) and registers it as "dotfiles", leaving every
// namespace in the catalogue but none materialized on disk — the state a
// bare `repo add` leaves things in, per concept.md "Sparse checkout".
func registerClonedCatalogue(t *testing.T, source string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	dataDir, err := paths.Data()
	if err != nil {
		t.Fatal(err)
	}
	_, resolvedURL, err := repo.Clone(dataDir, "file://"+source, "dotfiles")
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	reg := manifest.Registry{Repos: []manifest.Repo{{Name: "dotfiles", Owner: "someone", URL: resolvedURL}}}
	if err := manifest.WriteRegistry(reg); err != nil {
		t.Fatal(err)
	}
}

func TestEnableNamespace_NotInstalled_ErrorsNamingInstallFlag(t *testing.T) {
	home := t.TempDir()
	source := newCatalogueSourceRepo(t, home)
	registerClonedCatalogue(t, source)

	err := enableNamespace("editors", shared.Flags{})
	if err == nil {
		t.Fatal("expected enabling an uninstalled namespace to fail")
	}
	if !strings.Contains(err.Error(), "-i") {
		t.Fatalf("expected the error to name -i, got: %v", err)
	}
}

func TestEnableNamespace_Install_InstallsAndEnables(t *testing.T) {
	home := t.TempDir()
	source := newCatalogueSourceRepo(t, home)
	registerClonedCatalogue(t, source)

	if err := enableNamespace("editors", shared.Flags{Install: true}); err != nil {
		t.Fatalf("enable editors -i: %v", err)
	}

	dest := filepath.Join(home, ".editors")
	info, err := os.Lstat(dest)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected editors linked at %s, got err=%v", dest, err)
	}

	s, err := state.Read()
	if err != nil {
		t.Fatal(err)
	}
	if !s.Entries[state.Key{Repo: "dotfiles", Namespace: "editors"}].Enabled {
		t.Fatal("expected editors recorded enabled")
	}
}

func TestEnableNamespaces_MultipleWithInstall_AlreadyInstalledIsNotAnError(t *testing.T) {
	home := t.TempDir()
	source := newCatalogueSourceRepo(t, home)
	registerClonedCatalogue(t, source)

	// Pre-install "shell" via the install verb, leaving "editors" uninstalled.
	if err := installNamespaces([]string{"shell"}, shared.Flags{}); err != nil {
		t.Fatalf("install shell: %v", err)
	}

	if err := enableNamespaces([]string{"editors", "shell"}, shared.Flags{Install: true}); err != nil {
		t.Fatalf("enable editors shell -i: %v", err)
	}

	for _, ns := range []string{"editors", "shell"} {
		dest := filepath.Join(home, "."+ns)
		info, err := os.Lstat(dest)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("expected %s linked at %s, got err=%v", ns, dest, err)
		}
	}
}

func TestUninstallNamespaces_Enabled_DisablesAndUninstallsNoDanglingSymlink(t *testing.T) {
	home := t.TempDir()
	source := newCatalogueSourceRepo(t, home)
	registerClonedCatalogue(t, source)

	if err := enableNamespace("editors", shared.Flags{Install: true}); err != nil {
		t.Fatalf("enable editors -i: %v", err)
	}
	dest := filepath.Join(home, ".editors")
	if _, err := os.Lstat(dest); err != nil {
		t.Fatalf("expected editors linked before uninstall: %v", err)
	}

	if err := uninstallNamespaces([]string{"editors"}, shared.Flags{Yes: true}); err != nil {
		t.Fatalf("uninstall editors: %v", err)
	}

	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatalf("expected no dangling symlink at %s after uninstall, got err=%v", dest, err)
	}

	dataDir, err := paths.Data()
	if err != nil {
		t.Fatal(err)
	}
	nsDir := filepath.Join(dataDir, "dotfiles", "editors")
	if _, err := os.Stat(nsDir); !os.IsNotExist(err) {
		t.Fatalf("expected editors' folder gone from disk after uninstall, got err=%v", err)
	}

	s, err := state.Read()
	if err != nil {
		t.Fatal(err)
	}
	if s.Entries[state.Key{Repo: "dotfiles", Namespace: "editors"}].Enabled {
		t.Fatal("expected editors recorded disabled after uninstall")
	}
}

// TestUninstallNamespaces_DirtyInsideNamespace_RefusesForceSkipsOurGate
// covers the namespace-scoped git-safety gate: dirt inside the namespace
// being uninstalled refuses, naming it and pointing at `dots sync`; --force
// skips that gate specifically (a different, lower-level git protection —
// git's own sparse-checkout refusal to discard a dirty tracked/untracked
// path — is untouched by our --force and still applies, which is why the
// --force attempt below still errors, just with a different message).
func TestUninstallNamespaces_DirtyInsideNamespace_RefusesForceSkipsOurGate(t *testing.T) {
	home := t.TempDir()
	source := newCatalogueSourceRepo(t, home)
	registerClonedCatalogue(t, source)

	if err := installNamespaces([]string{"editors"}, shared.Flags{}); err != nil {
		t.Fatalf("install editors: %v", err)
	}

	dataDir, err := paths.Data()
	if err != nil {
		t.Fatal(err)
	}
	repoDir := filepath.Join(dataDir, "dotfiles")
	if err := os.WriteFile(filepath.Join(repoDir, "editors", "untracked"), []byte("dirty"), 0644); err != nil {
		t.Fatal(err)
	}

	err = uninstallNamespaces([]string{"editors"}, shared.Flags{Yes: true})
	if err == nil {
		t.Fatal("expected uninstall to refuse against a namespace with uncommitted changes")
	}
	if !strings.Contains(err.Error(), "sync") || !strings.Contains(err.Error(), "editors") {
		t.Fatalf("expected the error to point at `dots sync` and name editors, got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(repoDir, "editors")); statErr != nil {
		t.Fatalf("expected the namespace untouched after refusal, got err=%v", statErr)
	}

	err = uninstallNamespaces([]string{"editors"}, shared.Flags{Force: true})
	if err == nil || strings.Contains(err.Error(), "sync") {
		t.Fatalf("expected --force to skip our git-safety gate (a different error, from git's own sparse-checkout refusal), got: %v", err)
	}
}

// TestUninstallNamespaces_DirtyOutsideNamespace_NotBlocked covers the other
// half of the same scoping fix: uncommitted changes at the repository root
// — outside any namespace folder — must never block uninstalling an
// unrelated, clean namespace.
func TestUninstallNamespaces_DirtyOutsideNamespace_NotBlocked(t *testing.T) {
	home := t.TempDir()
	source := newCatalogueSourceRepo(t, home)
	registerClonedCatalogue(t, source)

	if err := installNamespaces([]string{"editors"}, shared.Flags{}); err != nil {
		t.Fatalf("install editors: %v", err)
	}

	dataDir, err := paths.Data()
	if err != nil {
		t.Fatal(err)
	}
	repoDir := filepath.Join(dataDir, "dotfiles")
	if err := os.WriteFile(filepath.Join(repoDir, "untracked"), []byte("dirty"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := uninstallNamespaces([]string{"editors"}, shared.Flags{Yes: true}); err != nil {
		t.Fatalf("expected repo-root dirt not to block uninstalling editors, got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(repoDir, "editors")); !os.IsNotExist(statErr) {
		t.Fatalf("expected editors uninstalled, got err=%v", statErr)
	}
}

// TestInstallNamespaces_MidBatchFailure_StillReportsCompletedItems covers a
// code-review finding: installNamespaces accumulates a report line per
// namespace as it materializes each one, but used to return the mid-batch
// error before ever printing what had already succeeded — leaving a user
// with no indication that "editors" really was materialized to disk even
// though the command as a whole failed. "editors" resolves and
// materializes fine; the second name doesn't exist in the catalogue, so
// resolution fails partway through the batch. The already-completed
// "editors" line must still reach stdout before the error propagates.
func TestInstallNamespaces_MidBatchFailure_StillReportsCompletedItems(t *testing.T) {
	home := t.TempDir()
	source := newCatalogueSourceRepo(t, home)
	registerClonedCatalogue(t, source)

	var err error
	stdout, _ := captureStdoutStderr(t, func() {
		err = installNamespaces([]string{"editors", "does-not-exist"}, shared.Flags{})
	})
	if err == nil {
		t.Fatal("expected installNamespaces to fail on the unresolvable second name")
	}
	if strings.TrimSpace(stdout) != "- editors" {
		t.Fatalf("expected the completed \"editors\" install still reported before the error, got %q", stdout)
	}

	dataDir, derr := paths.Data()
	if derr != nil {
		t.Fatal(derr)
	}
	if _, statErr := os.Stat(filepath.Join(dataDir, "dotfiles", "editors")); statErr != nil {
		t.Fatalf("expected editors actually materialized on disk despite the later failure, got err=%v", statErr)
	}
}

func TestRmRepo_NoRemote_RefusesNamingRemoteAdd(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	dataDir := filepath.Join(dataHome, "ireallylovemydots")
	repoDir := filepath.Join(dataDir, "dotfiles")
	nsDir := filepath.Join(repoDir, "editors")
	if err := os.MkdirAll(filepath.Join(nsDir, "editors"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Write(nsDir, manifest.Manifest{Entries: []manifest.Entry{{Name: "editors", Dest: filepath.Join(t.TempDir(), "editors")}}}); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoDir, "init", "-b", "main")
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "init")

	reg := manifest.Registry{Repos: []manifest.Repo{{Name: "dotfiles", Origin: manifest.OriginLocal}}}
	if err := manifest.WriteRegistry(reg); err != nil {
		t.Fatal(err)
	}

	err := HandleRepo([]string{"rm", "dotfiles"}, shared.Flags{Yes: true})
	if err == nil {
		t.Fatal("expected repo rm to refuse a repository with no remote")
	}
	if !strings.Contains(err.Error(), "git remote add") {
		t.Fatalf("expected the error to name `git remote add`, got: %v", err)
	}
	if _, statErr := os.Stat(repoDir); statErr != nil {
		t.Fatalf("expected the repository left untouched, got err=%v", statErr)
	}

	if err := HandleRepo([]string{"rm", "dotfiles"}, shared.Flags{Yes: true, Force: true}); err != nil {
		t.Fatalf("repo rm dotfiles --force: %v", err)
	}
	if _, statErr := os.Stat(repoDir); !os.IsNotExist(statErr) {
		t.Fatalf("expected --force to proceed with the removal, got err=%v", statErr)
	}
}
