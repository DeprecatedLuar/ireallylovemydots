package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/state"
)

// TestHandleMv_DisabledSource_MovesAndSourceGone covers implementation-plan.md
// Phase 17's disabled-source case: mv behaves like cp, then removes the
// source namespace directory outright.
func TestHandleMv_DisabledSource_MovesAndSourceGone(t *testing.T) {
	dataDir := setupTwoRepos(t)
	srcDir := writeMaterializedNamespace(t, dataDir, "src", "nvim")

	if err := HandleMv([]string{"src/nvim", "dst/nvim"}, shared.Flags{}); err != nil {
		t.Fatalf("mv src/nvim dst/nvim: %v", err)
	}

	dstDir := filepath.Join(dataDir, "dst", "nvim")
	if _, err := os.Stat(filepath.Join(dstDir, "payload", "seed")); err != nil {
		t.Fatalf("expected payload moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dstDir, ".profiles", "work.toml")); err != nil {
		t.Fatalf("expected .profiles/ moved: %v", err)
	}
	if _, err := os.Stat(srcDir); !os.IsNotExist(err) {
		t.Fatalf("expected the source namespace directory gone, got err=%v", err)
	}
}

// TestHandleMv_EnabledSource_RelinksAndRemovesSource covers the enabled-source
// handoff: the destination ends enabled with every symlink repointed at it,
// and the source is gone, from both the filesystem and machine state.
func TestHandleMv_EnabledSource_RelinksAndRemovesSource(t *testing.T) {
	dataDir := setupTwoRepos(t)
	srcDir := writeMaterializedNamespace(t, dataDir, "src", "nvim")
	srcM, err := manifest.Read(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	dest := srcM.Entries[0].Dest

	if err := enableNamespace("nvim", shared.Flags{Repo: "src"}); err != nil {
		t.Fatalf("enable nvim: %v", err)
	}
	if target, err := os.Readlink(dest); err != nil || !strings.Contains(target, filepath.Join("src", "nvim")) {
		t.Fatalf("expected the symlink to point into src/nvim before the move, target=%q err=%v", target, err)
	}

	if err := HandleMv([]string{"src/nvim", "dst/nvim"}, shared.Flags{}); err != nil {
		t.Fatalf("mv src/nvim dst/nvim: %v", err)
	}

	target, err := os.Readlink(dest)
	if err != nil {
		t.Fatalf("expected the destination still linked: %v", err)
	}
	if !strings.Contains(target, filepath.Join("dst", "nvim")) {
		t.Fatalf("expected the symlink repointed into dst/nvim, got %q", target)
	}

	if _, err := os.Stat(srcDir); !os.IsNotExist(err) {
		t.Fatalf("expected the source namespace directory gone, got err=%v", err)
	}

	s, err := state.Read()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Entries[state.Key{Repo: "src", Namespace: "nvim"}]; ok {
		t.Fatal("expected the source's state entry removed")
	}
	dstEntry, ok := s.Entries[state.Key{Repo: "dst", Namespace: "nvim"}]
	if !ok || !dstEntry.Enabled {
		t.Fatalf("expected the destination enabled in state, got %+v (ok=%v)", dstEntry, ok)
	}
}

// TestHandleMv_ReadOnlySource_ErrorsAndChangesNothing covers the mv-specific
// precondition cp doesn't have: mv deletes the source, so a read-only source
// must be refused, not silently read out of git the way cp does.
func TestHandleMv_ReadOnlySource_ErrorsAndChangesNothing(t *testing.T) {
	dataDir := setupTwoRepos(t)
	srcDir := writeMaterializedNamespace(t, dataDir, "src", "nvim")

	access := state.Access{}
	access.SetReadOnly("src", true)
	if err := state.WriteAccess(access); err != nil {
		t.Fatal(err)
	}

	err := HandleMv([]string{"src/nvim", "dst/nvim"}, shared.Flags{})
	if err == nil {
		t.Fatal("expected mv out of a read-only source to error")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("expected the error to name read-only, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "dst", "nvim")); !os.IsNotExist(err) {
		t.Fatalf("expected nothing written to the destination, got err=%v", err)
	}
	if _, err := os.Stat(srcDir); err != nil {
		t.Fatalf("expected the source left untouched: %v", err)
	}
}

// TestHandleMv_PreflightFailureOnDestination_LeavesSourceEnabledAndIntact
// covers step 6.2 of implementation-plan.md Phase 17: a destination
// pre-flight problem that isn't the source's own expected claim on its
// destinations aborts the move before the source is touched at all.
func TestHandleMv_PreflightFailureOnDestination_LeavesSourceEnabledAndIntact(t *testing.T) {
	dataDir := setupTwoRepos(t)
	srcDir := writeMaterializedNamespace(t, dataDir, "src", "nvim")
	srcM, err := manifest.Read(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	goodDest := srcM.Entries[0].Dest

	if err := enableNamespace("nvim", shared.Flags{Repo: "src"}); err != nil {
		t.Fatalf("enable nvim: %v", err)
	}

	// Simulate a manual edit that tracked a second entry with a destination
	// pre-flight always refuses (a protected root), never itself enabled, so
	// it carries no state claim of its own and can't be mistaken for the
	// source's expected Collision.
	m, err := manifest.Read(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	m.Entries = append(m.Entries, manifest.Entry{Name: "bad", Dest: "/"})
	if err := manifest.Write(srcDir, m); err != nil {
		t.Fatal(err)
	}

	err = HandleMv([]string{"src/nvim", "dst/nvim"}, shared.Flags{})
	if err == nil {
		t.Fatal("expected the move to abort on the destination's pre-flight problem")
	}

	if _, statErr := os.Stat(filepath.Join(dataDir, "dst", "nvim")); !os.IsNotExist(statErr) {
		t.Fatalf("expected the partial copy removed, got err=%v", statErr)
	}

	s, err := state.Read()
	if err != nil {
		t.Fatal(err)
	}
	if !s.Entries[state.Key{Repo: "src", Namespace: "nvim"}].Enabled {
		t.Fatal("expected the source left enabled")
	}
	if target, err := os.Readlink(goodDest); err != nil || !strings.Contains(target, filepath.Join("src", "nvim")) {
		t.Fatalf("expected the source's symlink left intact, target=%q err=%v", target, err)
	}
	if _, err := os.Stat(srcDir); err != nil {
		t.Fatalf("expected the source namespace directory left intact: %v", err)
	}
}

// TestHandleMv_ExistingDestinationNamespace_Errors mirrors cp's own
// precondition test: mv shares the same "no overwriting" rule.
func TestHandleMv_ExistingDestinationNamespace_Errors(t *testing.T) {
	dataDir := setupTwoRepos(t)
	writeMaterializedNamespace(t, dataDir, "src", "nvim")
	writeMaterializedNamespace(t, dataDir, "dst", "nvim")

	err := HandleMv([]string{"src/nvim", "dst/nvim"}, shared.Flags{})
	if err == nil {
		t.Fatal("expected mv onto an existing destination namespace to error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected the error to name the collision, got: %v", err)
	}
}
