package commands

import (
	"strings"
	"testing"

	"github.com/DeprecatedLuar/ireallylovemydots/internal/commands/shared"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/state"
)

func TestHandleSyncMode_SetShowAndClear(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	newRegisteredRepo(t, dataDir, scratchRoot, "repo", []string{"a"})
	key := state.Key{Repo: "repo", Namespace: "a"}

	if err := handleSyncMode("a", []string{"overlay"}, shared.Flags{}); err != nil {
		t.Fatal(err)
	}
	s, _ := state.Read()
	if s.Entries[key].SyncMode != "overlay" {
		t.Fatalf("SyncMode = %q, want overlay", s.Entries[key].SyncMode)
	}
	stdout, _ := captureStdoutStderr(t, func() { _ = handleSyncMode("a", nil, shared.Flags{}) })
	if !strings.Contains(stdout, "overlay") {
		t.Fatalf("show printed %q", stdout)
	}
	if err := handleSyncMode("a", []string{"merge"}, shared.Flags{}); err != nil {
		t.Fatal(err)
	}
	s, _ = state.Read()
	if s.Entries[key].SyncMode != "" {
		t.Fatal("merge must clear the saved mode")
	}
}

func TestHandleSyncMode_RejectsUnknownAndReadOnlyOverwriteRemote(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	newRegisteredRepo(t, dataDir, scratchRoot, "repo", []string{"a"})
	if err := handleSyncMode("a", []string{"local"}, shared.Flags{}); err == nil {
		t.Fatal("expected an unknown mode to be refused")
	}
	markReadOnly(t, "repo")
	if err := handleSyncMode("a", []string{"overwrite-remote"}, shared.Flags{}); err == nil {
		t.Fatal("expected overwrite-remote to be refused on a read-only repository")
	}
}
