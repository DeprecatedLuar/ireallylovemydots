package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/manifest"
)

func gitStatusPorcelain(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func gitLog(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "log", "--oneline").CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v\n%s", err, out)
	}
	return string(out)
}

func gitCheckoutDot(t *testing.T, dir string) {
	t.Helper()
	gitRun(t, dir, "checkout", ".")
	gitRun(t, dir, "clean", "-fd")
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// nvimKittyBashrcRepo builds the concept.md worked example: nvim/, kitty/,
// and .bashrc at the root of a committed git repository, with no namespace
// among them.
func nvimKittyBashrcRepo(t *testing.T) string {
	t.Helper()
	dir := initGitRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, "nvim"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "nvim", "init.lua"), "-- nvim config")
	if err := os.MkdirAll(filepath.Join(dir, "kitty"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "kitty", "kitty.conf"), "# kitty config")
	writeFile(t, filepath.Join(dir, ".bashrc"), "# bashrc")
	commitAll(t, dir)
	return dir
}

func TestPlan_NvimKittyBashrc_ProducesThreeNamespaces(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := nvimKittyBashrcRepo(t)

	plan, err := Plan(dir)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan) != 3 {
		t.Fatalf("expected 3 planned namespaces, got %+v", plan)
	}

	byNamespace := map[string]PlannedNamespace{}
	for _, p := range plan {
		byNamespace[p.Namespace] = p
	}

	nvim, ok := byNamespace["nvim"]
	if !ok || nvim.EntryName != "nvim" || nvim.Dest != filepath.Join(home, ".config", "nvim") {
		t.Fatalf("expected nvim -> ~/.config/nvim, got %+v (ok=%v)", nvim, ok)
	}
	kitty, ok := byNamespace["kitty"]
	if !ok || kitty.EntryName != "kitty" || kitty.Dest != filepath.Join(home, ".config", "kitty") {
		t.Fatalf("expected kitty -> ~/.config/kitty, got %+v (ok=%v)", kitty, ok)
	}
	bashrc, ok := byNamespace["bashrc"]
	if !ok || bashrc.EntryName != ".bashrc" || bashrc.Dest != filepath.Join(home, ".bashrc") {
		t.Fatalf("expected bashrc -> ~/.bashrc, got %+v (ok=%v)", bashrc, ok)
	}
}

func TestPlan_SkipsGitForgeAndDocFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := initGitRepo(t)
	writeFile(t, filepath.Join(dir, "README.md"), "hi")
	writeFile(t, filepath.Join(dir, "LICENSE"), "mit")
	if err := os.MkdirAll(filepath.Join(dir, ".github"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ".github", "workflow.yml"), "x")
	writeFile(t, filepath.Join(dir, ".gitignore"), "x")
	writeFile(t, filepath.Join(dir, "dotloadout.work.toml"), "x")
	commitAll(t, dir)

	plan, err := Plan(dir)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan) != 0 {
		t.Fatalf("expected no namespaces from README/LICENSE/.github/.gitignore/loadout, got %+v", plan)
	}
}

func TestPlan_AlreadyNamespacedRepositoryProposesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := initGitRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, "editors"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "editors", ".dots"), "")
	commitAll(t, dir)

	plan, err := Plan(dir)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan) != 0 {
		t.Fatalf("expected nothing to convert on an already-namespaced repository, got %+v", plan)
	}
}

func TestPlan_TwoNamesResolvingToOneNamespace_RefusedNamingBoth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := initGitRepo(t)
	writeFile(t, filepath.Join(dir, ".bashrc"), "# bashrc")
	if err := os.MkdirAll(filepath.Join(dir, "bashrc"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "bashrc", "extra"), "x")
	commitAll(t, dir)

	_, err := Plan(dir)
	if err == nil {
		t.Fatal("expected an error for .bashrc and bashrc/ colliding")
	}
	if !strings.Contains(err.Error(), ".bashrc") || !strings.Contains(err.Error(), "bashrc") {
		t.Fatalf("expected the error to name both entries, got: %v", err)
	}
}

// TestPlan_FileExtensionDroppedFromNamespaceNameOnly checks that a
// single-file entry like starship.toml becomes the namespace `starship`
// while the entry and its destination keep the extension, and that a
// directory whose name merely looks extensioned keeps it whole.
func TestPlan_FileExtensionDroppedFromNamespaceNameOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := initGitRepo(t)
	writeFile(t, filepath.Join(dir, "starship.toml"), "# starship")
	writeFile(t, filepath.Join(dir, ".p10k.zsh"), "# p10k")
	writeFile(t, filepath.Join(dir, ".zshrc"), "# zshrc")
	if err := os.MkdirAll(filepath.Join(dir, "nvim.d"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "nvim.d", "init.lua"), "-- init")
	commitAll(t, dir)

	plan, err := Plan(dir)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	byNamespace := map[string]PlannedNamespace{}
	for _, p := range plan {
		byNamespace[p.Namespace] = p
	}

	starship, ok := byNamespace["starship"]
	if !ok {
		t.Fatalf("expected namespace starship, got %+v", plan)
	}
	if starship.EntryName != "starship.toml" {
		t.Fatalf("expected the entry to keep its extension, got %s", starship.EntryName)
	}
	if want := filepath.Join(home, ".config", "starship.toml"); starship.Dest != want {
		t.Fatalf("expected dest %s, got %s", want, starship.Dest)
	}

	p10k, ok := byNamespace["p10k"]
	if !ok {
		t.Fatalf("expected namespace p10k, got %+v", plan)
	}
	if want := filepath.Join(home, ".p10k.zsh"); p10k.Dest != want {
		t.Fatalf("expected dest %s, got %s", want, p10k.Dest)
	}

	if _, ok := byNamespace["zshrc"]; !ok {
		t.Fatalf("expected .zshrc to stay namespace zshrc, got %+v", plan)
	}
	if _, ok := byNamespace["nvim.d"]; !ok {
		t.Fatalf("expected directory nvim.d to keep its name, got %+v", plan)
	}
}

func TestPlan_ConfigHomeSplit_PreviewsOddlyButCorrectly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := initGitRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "config", "x"), "x")
	if err := os.MkdirAll(filepath.Join(dir, "home"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "home", "y"), "y")
	commitAll(t, dir)

	plan, err := Plan(dir)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan) != 2 {
		t.Fatalf("expected 2 planned namespaces, got %+v", plan)
	}
	byNamespace := map[string]PlannedNamespace{}
	for _, p := range plan {
		byNamespace[p.Namespace] = p
	}
	if got := byNamespace["config"].Dest; got != filepath.Join(home, ".config", "config") {
		t.Fatalf("expected config -> ~/.config/config, got %s", got)
	}
	if got := byNamespace["home"].Dest; got != filepath.Join(home, ".config", "home") {
		t.Fatalf("expected home -> ~/.config/home, got %s", got)
	}
}

func TestApply_CreatesNamespacesMovesEntriesAndWritesManifests(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := nvimKittyBashrcRepo(t)

	plan, err := Plan(dir)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if err := Apply(dir, plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	for _, want := range []struct {
		namespace, entryName, dest string
	}{
		{"nvim", "nvim", filepath.Join(home, ".config", "nvim")},
		{"kitty", "kitty", filepath.Join(home, ".config", "kitty")},
		{"bashrc", ".bashrc", filepath.Join(home, ".bashrc")},
	} {
		nsDir := filepath.Join(dir, want.namespace)
		if _, err := os.Stat(filepath.Join(nsDir, want.entryName)); err != nil {
			t.Fatalf("expected %s moved into %s: %v", want.entryName, nsDir, err)
		}
		m, err := manifest.Read(nsDir)
		if err != nil {
			t.Fatalf("read manifest for %s: %v", want.namespace, err)
		}
		if len(m.Entries) != 1 || m.Entries[0].Name != want.entryName || m.Entries[0].Dest != want.dest {
			t.Fatalf("unexpected manifest for %s: %+v", want.namespace, m.Entries)
		}
	}

	if _, err := os.Stat(filepath.Join(dir, "nvim", "nvim")); err != nil {
		t.Fatalf("nested payload missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".bashrc")); !os.IsNotExist(err) {
		t.Fatalf("expected .bashrc moved out of the root, got err=%v", err)
	}
}

func TestApply_NeverCommitsAndGitCheckoutRestoresExactly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := nvimKittyBashrcRepo(t)

	plan, err := Plan(dir)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if err := Apply(dir, plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	statusBefore := gitStatusPorcelain(t, dir)
	if statusBefore == "" {
		t.Fatal("expected Apply to leave the working tree dirty")
	}
	logBefore := gitLog(t, dir)

	gitCheckoutDot(t, dir)

	if _, err := os.Stat(filepath.Join(dir, ".bashrc")); err != nil {
		t.Fatalf("expected .bashrc restored to the root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "nvim", "nvim")); !os.IsNotExist(err) {
		t.Fatalf("expected the bootstrap-created namespace folder gone after checkout, got err=%v", err)
	}
	if got := gitLog(t, dir); got != logBefore {
		t.Fatalf("expected no commit made by Apply, log changed:\nbefore=%q\nafter=%q", logBefore, got)
	}
}
