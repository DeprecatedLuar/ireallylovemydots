package namespace

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRebuildFromLinks_ProvenSymlinkReconstructed covers the core claim: a
// destination that is still a symlink pointing inside the namespace folder
// hands back both halves of its entry — proof, not inference.
func TestRebuildFromLinks_ProvenSymlinkReconstructed(t *testing.T) {
	nsDir := t.TempDir()
	payload := filepath.Join(nsDir, "cfg")
	if err := os.WriteFile(payload, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "cfg")
	if err := os.Symlink(payload, dest); err != nil {
		t.Fatal(err)
	}

	entries := RebuildFromLinks(nsDir, []string{dest})
	if len(entries) != 1 || entries[0].Name != "cfg" || entries[0].Dest != dest {
		t.Fatalf("expected one entry {cfg, %s}, got %+v", dest, entries)
	}
}

// TestRebuildFromLinks_SkipsUnproven covers every case that contributes
// nothing: a destination that no longer exists, and a symlink that points
// somewhere other than this namespace's folder — neither is proof this
// namespace owns it.
func TestRebuildFromLinks_SkipsUnproven(t *testing.T) {
	nsDir := t.TempDir()
	missing := filepath.Join(t.TempDir(), "gone")

	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(elsewhere, []byte("y"), 0644); err != nil {
		t.Fatal(err)
	}
	repointed := filepath.Join(t.TempDir(), "repointed")
	if err := os.Symlink(elsewhere, repointed); err != nil {
		t.Fatal(err)
	}

	entries := RebuildFromLinks(nsDir, []string{missing, repointed})
	if len(entries) != 0 {
		t.Fatalf("expected no entries reconstructed, got %+v", entries)
	}
}

// TestScaffold_UntrackedBecomesDestless covers the last rung of the ladder:
// every payload Inspect could not otherwise account for becomes a dest-less
// entry, so it lists as "!" rather than vanishing from the manifest.
func TestScaffold_UntrackedBecomesDestless(t *testing.T) {
	report := Report{Untracked: []string{"a", "b"}}
	entries := Scaffold(report)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %+v", entries)
	}
	for _, e := range entries {
		if e.Dest != "" {
			t.Fatalf("expected scaffolded entry %q to have an empty destination, got %q", e.Name, e.Dest)
		}
	}
}
