package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/state"
)

// trackFixture registers one empty namespace and returns its directory plus
// a real file outside the data directory to track into it.
func trackFixture(t *testing.T, fileName string) (nsName, nsDir, dest string) {
	t.Helper()
	nsName = setupRegisteredNamespace(t)
	dataHome := os.Getenv("XDG_DATA_HOME")
	nsDir = filepath.Join(dataHome, "ireallylovemydots", "dotfiles", nsName)

	dest = filepath.Join(t.TempDir(), fileName)
	if err := os.WriteFile(dest, []byte("content of "+fileName), 0644); err != nil {
		t.Fatal(err)
	}
	return nsName, nsDir, dest
}

func trackState(t *testing.T, nsName string) state.Entry {
	t.Helper()
	s, err := state.Read()
	if err != nil {
		t.Fatal(err)
	}
	return s.Entries[state.Key{Repo: "dotfiles", Namespace: nsName}]
}

func assertLinked(t *testing.T, dest, payload string) {
	t.Helper()
	target, err := os.Readlink(dest)
	if err != nil {
		t.Fatalf("expected a symlink at %s: %v", dest, err)
	}
	if target != payload {
		t.Fatalf("symlink target = %s, want %s", target, payload)
	}
}

// A namespace with no state entry has never been enabled or disabled here,
// so "disabled" is a default rather than a declaration: tracking into it
// enables it, and the links are recorded in state like any other enable.
func TestTrackPaths_NoStateEntryEnablesAndRecordsTheLink(t *testing.T) {
	nsName, nsDir, dest := trackFixture(t, "init.lua")

	captureStdoutStderr(t, func() {
		if err := trackPaths(nsName, []string{dest}, shared.Flags{}); err != nil {
			t.Fatalf("trackPaths: %v", err)
		}
	})

	assertLinked(t, dest, filepath.Join(nsDir, "init.lua"))
	entry := trackState(t, nsName)
	if !entry.Enabled {
		t.Fatal("expected the namespace to be enabled by its first add")
	}
	if len(entry.LinkedDests) != 1 || entry.LinkedDests[0] != dest {
		t.Fatalf("LinkedDests = %v, want [%s]", entry.LinkedDests, dest)
	}
}

// An already-enabled namespace links the new entry alongside the existing
// ones, and state grows to name both.
func TestTrackPaths_EnabledNamespaceLinksTheNewEntryToo(t *testing.T) {
	nsName, nsDir, first := trackFixture(t, "init.lua")
	second := filepath.Join(filepath.Dir(first), "colors.lua")
	if err := os.WriteFile(second, []byte("colors"), 0644); err != nil {
		t.Fatal(err)
	}

	captureStdoutStderr(t, func() {
		if err := trackPaths(nsName, []string{first}, shared.Flags{}); err != nil {
			t.Fatalf("trackPaths (first): %v", err)
		}
		if err := trackPaths(nsName, []string{second}, shared.Flags{}); err != nil {
			t.Fatalf("trackPaths (second): %v", err)
		}
	})

	assertLinked(t, first, filepath.Join(nsDir, "init.lua"))
	assertLinked(t, second, filepath.Join(nsDir, "colors.lua"))
	entry := trackState(t, nsName)
	if !entry.Enabled || len(entry.LinkedDests) != 2 {
		t.Fatalf("state entry = %+v, want enabled with both destinations", entry)
	}
}

// A namespace the user explicitly disabled stays disabled: the payload is
// tracked and moved in, but nothing is linked. Linking here would leave
// live symlinks under a namespace state calls disabled — the exact desync
// `disable` would then have no record of and could never clean up.
func TestTrackPaths_DisabledNamespaceTracksWithoutLinking(t *testing.T) {
	nsName, nsDir, dest := trackFixture(t, "init.lua")

	captureStdoutStderr(t, func() {
		if err := disableNamespace(nsName, shared.Flags{}); err != nil {
			t.Fatalf("disable: %v", err)
		}
		if err := trackPaths(nsName, []string{dest}, shared.Flags{}); err != nil {
			t.Fatalf("trackPaths: %v", err)
		}
	})

	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatalf("expected nothing at %s while the namespace is disabled, got err = %v", dest, err)
	}
	if _, err := os.Stat(filepath.Join(nsDir, "init.lua")); err != nil {
		t.Fatalf("expected the payload tracked into the namespace anyway: %v", err)
	}
	m, err := manifest.Read(nsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entries) != 1 || m.Entries[0].Dest != dest {
		t.Fatalf("manifest entries = %+v, want one entry for %s", m.Entries, dest)
	}
	entry := trackState(t, nsName)
	if entry.Enabled || len(entry.LinkedDests) != 0 {
		t.Fatalf("state entry = %+v, want it left disabled with no links", entry)
	}
}

// The same-directory alias case, end to end: the alias keeps pointing at
// its sibling once both live in the namespace, and the destination links
// through to the tracked content.
func TestTrackPaths_SameDirectoryAliasResolvesAfterLinking(t *testing.T) {
	nsName, nsDir, claudeMd := trackFixture(t, "CLAUDE.md")
	agentsMd := filepath.Join(filepath.Dir(claudeMd), "AGENTS.md")
	if err := os.Symlink("CLAUDE.md", agentsMd); err != nil {
		t.Fatal(err)
	}

	captureStdoutStderr(t, func() {
		if err := trackPaths(nsName, []string{claudeMd, agentsMd}, shared.Flags{}); err != nil {
			t.Fatalf("trackPaths: %v", err)
		}
	})

	assertLinked(t, agentsMd, filepath.Join(nsDir, "AGENTS.md"))
	data, err := os.ReadFile(agentsMd)
	if err != nil || string(data) != "content of CLAUDE.md" {
		t.Fatalf("AGENTS.md resolved to %q, err = %v", data, err)
	}
}
