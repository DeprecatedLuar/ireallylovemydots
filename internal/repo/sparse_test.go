package repo

import (
	"os"
	"path/filepath"
	"testing"
)

// newMultiNamespaceRepo builds a source repository with count namespace
// folders (ns1, ns2, ...), each holding a .dots manifest and one file, plus
// a root README — standing in for a real dotfiles remote with several
// namespaces.
func newMultiNamespaceRepo(t *testing.T, count int) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	gitRun(t, dir, "config", "user.email", "test@example.com")
	gitRun(t, dir, "config", "user.name", "test")

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("root\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= count; i++ {
		name := nsName(i)
		nsDir := filepath.Join(dir, name)
		if err := os.MkdirAll(nsDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(nsDir, ".dots"), []byte("[]"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(nsDir, "file.txt"), []byte(name+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func nsName(i int) string {
	return "ns" + string(rune('0'+i))
}

// sparseClone clones source into a fresh directory the way repo.Clone does
// (blobless, --sparse, empty cone), skipping through repo.Clone itself so
// these tests exercise sparse.go's primitives directly against a real
// clone rather than depending on Clone's own behavior.
func sparseClone(t *testing.T, source string) string {
	t.Helper()
	dest := filepath.Join(t.TempDir(), "clone")
	gitRun(t, "", "clone", "--filter=blob:none", "--sparse", "file://"+source, dest)
	return dest
}

func TestInit_EmptyCone(t *testing.T) {
	source := newMultiNamespaceRepo(t, 2)
	dest := t.TempDir()
	gitRun(t, "", "clone", "--filter=blob:none", "--no-checkout", "file://"+source, dest)

	if err := Init(dest); err != nil {
		t.Fatalf("Init: %v", err)
	}
	sparse, err := IsSparse(dest)
	if err != nil {
		t.Fatalf("IsSparse: %v", err)
	}
	if !sparse {
		t.Fatal("expected repository to be sparse after Init")
	}
	cone, err := List(dest)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(cone) != 0 {
		t.Fatalf("List after Init = %v, want empty cone", cone)
	}
}

func TestIsSparse_NonSparseRepo(t *testing.T) {
	source := newMultiNamespaceRepo(t, 1)
	sparse, err := IsSparse(source)
	if err != nil {
		t.Fatalf("IsSparse: %v", err)
	}
	if sparse {
		t.Fatal("expected a plain repository to report non-sparse")
	}
}

// TestSuccessCriterion1_OneInstalledIsClean covers: a 4-namespace repository
// with 1 installed reports a completely clean `git status`.
func TestSuccessCriterion1_OneInstalledIsClean(t *testing.T) {
	source := newMultiNamespaceRepo(t, 4)
	dest := sparseClone(t, source)

	if err := Add(dest, "ns1"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "ns1")); err != nil {
		t.Fatalf("expected ns1 materialized: %v", err)
	}
	for _, absent := range []string{"ns2", "ns3", "ns4"} {
		if _, err := os.Stat(filepath.Join(dest, absent)); !os.IsNotExist(err) {
			t.Fatalf("expected %s to stay absent, err=%v", absent, err)
		}
	}
	if status := gitStatusPorcelain(t, dest); status != "" {
		t.Fatalf("git status --porcelain = %q, want clean", status)
	}
}

// TestSuccessCriterion2_InstallThenUninstallStaysClean covers: installing
// (Add) then uninstalling (Remove) a namespace leaves `git status` clean in
// both directions.
func TestSuccessCriterion2_InstallThenUninstallStaysClean(t *testing.T) {
	source := newMultiNamespaceRepo(t, 3)
	dest := sparseClone(t, source)

	if err := Add(dest, "ns2"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if status := gitStatusPorcelain(t, dest); status != "" {
		t.Fatalf("git status after Add = %q, want clean", status)
	}

	if err := Remove(dest, "ns2"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "ns2")); !os.IsNotExist(err) {
		t.Fatalf("expected ns2 removed from the working tree, err=%v", err)
	}
	if status := gitStatusPorcelain(t, dest); status != "" {
		t.Fatalf("git status after Remove = %q, want clean", status)
	}
}

// TestSuccessCriterion3_EnsureSparseConvertsNonSparseRepo covers: a
// repository converted from fully-checked-out (non-sparse) via EnsureSparse
// keeps every namespace folder that was on disk and loses none, and `git
// status` goes from a staged-deletions-look to clean.
func TestSuccessCriterion3_EnsureSparseConvertsNonSparseRepo(t *testing.T) {
	source := newMultiNamespaceRepo(t, 3)
	dest := t.TempDir()
	// A plain, fully checked out clone (no --sparse, no --no-checkout):
	// what a pre-8.7 clone or a hand-copied repository looks like.
	gitRun(t, "", "clone", "file://"+source, dest)

	if sparse, err := IsSparse(dest); err != nil || sparse {
		t.Fatalf("expected a freshly cloned plain repository to be non-sparse, sparse=%v err=%v", sparse, err)
	}

	cone := []string{"ns1", "ns2", "ns3"}
	if err := EnsureSparse(dest, cone); err != nil {
		t.Fatalf("EnsureSparse: %v", err)
	}
	for _, name := range cone {
		if _, err := os.Stat(filepath.Join(dest, name)); err != nil {
			t.Fatalf("expected %s to remain on disk after conversion: %v", name, err)
		}
	}
	if status := gitStatusPorcelain(t, dest); status != "" {
		t.Fatalf("git status after EnsureSparse = %q, want clean", status)
	}
	sparse, err := IsSparse(dest)
	if err != nil {
		t.Fatalf("IsSparse: %v", err)
	}
	if !sparse {
		t.Fatal("expected repository to be sparse after EnsureSparse")
	}
}

// TestSuccessCriterion5_RemoveRefusesDirtyNamespace covers: uninstalling a
// namespace holding an uncommitted edit does not remove it from the cone,
// and the caller can tell — Remove reports failure rather than silently
// doing nothing.
func TestSuccessCriterion5_RemoveRefusesDirtyNamespace(t *testing.T) {
	source := newMultiNamespaceRepo(t, 2)
	dest := sparseClone(t, source)

	if err := Add(dest, "ns1", "ns2"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	dirtyFile := filepath.Join(dest, "ns1", "file.txt")
	if err := os.WriteFile(dirtyFile, []byte("dirty\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := Remove(dest, "ns1"); err == nil {
		t.Fatal("expected Remove to refuse a namespace with an uncommitted edit")
	}
	if _, err := os.Stat(filepath.Join(dest, "ns1")); err != nil {
		t.Fatalf("expected ns1 to remain on disk after refused removal: %v", err)
	}
	content, err := os.ReadFile(dirtyFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "dirty\n" {
		t.Fatalf("dirty file content = %q, want preserved", content)
	}

	// A clean namespace passed alongside a dirty one in the same call
	// should not be blocked by the dirty one's refusal.
	if err := Remove(dest, "ns1", "ns2"); err == nil {
		t.Fatal("expected Remove to still report the ns1 refusal")
	}
	if _, err := os.Stat(filepath.Join(dest, "ns2")); !os.IsNotExist(err) {
		t.Fatalf("expected clean ns2 to still be removed despite ns1's refusal, err=%v", err)
	}
}

func TestReapply(t *testing.T) {
	source := newMultiNamespaceRepo(t, 1)
	dest := sparseClone(t, source)
	if err := Add(dest, "ns1"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := Reapply(dest); err != nil {
		t.Fatalf("Reapply: %v", err)
	}
	if status := gitStatusPorcelain(t, dest); status != "" {
		t.Fatalf("git status after Reapply = %q, want clean", status)
	}
}

func TestList_ReflectsAddedNamespaces(t *testing.T) {
	source := newMultiNamespaceRepo(t, 3)
	dest := sparseClone(t, source)
	if err := Add(dest, "ns1", "ns3"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	cone, err := List(dest)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := map[string]bool{}
	for _, c := range cone {
		got[c] = true
	}
	if !got["ns1"] || !got["ns3"] || got["ns2"] {
		t.Fatalf("List = %v, want exactly [ns1 ns3]", cone)
	}
}
