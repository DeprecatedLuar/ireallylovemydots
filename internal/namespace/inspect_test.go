package namespace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/manifest"
)

// TestInspect_GitignoreNotUntracked covers the fix for the reported bug: a
// namespace's own .gitignore is repo plumbing, not payload, and must not be
// reported as an untracked file — every namespace holding one was
// incorrectly rolling up to "!" before this.
func TestInspect_GitignoreNotUntracked(t *testing.T) {
	nsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(nsDir, "cfg"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nsDir, ".gitignore"), []byte("ignored/\n"), 0644); err != nil {
		t.Fatal(err)
	}
	entries := []manifest.Entry{{Name: "cfg", Dest: "/tmp/wherever"}}
	if err := manifest.Write(nsDir, manifest.Manifest{Entries: entries}); err != nil {
		t.Fatal(err)
	}

	report, err := Inspect(nsDir, entries)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Untracked) != 0 {
		t.Fatalf("expected .gitignore excluded from Untracked, got %v", report.Untracked)
	}
	if report.ManifestMissing {
		t.Fatalf("expected ManifestMissing false, .dots is present")
	}
}

// TestInspect_ManifestMissingDetected covers the distinction Read
// deliberately erases: no .dots file at all reads differently from one that
// parses to zero entries.
func TestInspect_ManifestMissingDetected(t *testing.T) {
	nsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(nsDir, "cfg"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	report, err := Inspect(nsDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !report.ManifestMissing {
		t.Fatalf("expected ManifestMissing true when no .dots file exists")
	}
	if len(report.Untracked) != 1 || report.Untracked[0] != "cfg" {
		t.Fatalf("expected [cfg] untracked, got %v", report.Untracked)
	}
}
