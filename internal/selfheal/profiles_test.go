package selfheal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/profile"
	"github.com/DeprecatedLuar/dotz/internal/state"
)

func setActiveProfile(t *testing.T, name string, dest string) {
	t.Helper()
	s, err := state.Read()
	if err != nil {
		t.Fatal(err)
	}
	key := state.Key{Repo: "dotfiles", Namespace: "cfg-ns"}
	entry := s.Entries[key]
	entry.ActiveProfile = name
	entry.LinkedDests = []string{dest}
	s.Entries[key] = entry
	if err := state.Write(s); err != nil {
		t.Fatal(err)
	}
}

// An entry declared profiled but no longer tracked by the namespace
// manifest is a clean merge of two committed files disagreeing: warned, and
// never written back.
func TestRun_ProfiledEntryMissingFromManifest_IsWarnedNotRepaired(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".config", "cfg")
	namespaceDir, payload := setupNamespace(t, dest)
	mustSymlink(t, payload, dest)

	if err := profile.Write(namespaceDir, profile.Manifest{Entries: []string{"cfg", "gone"}}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(profile.Path(namespaceDir))
	if err != nil {
		t.Fatal(err)
	}

	findings, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var found bool
	for _, p := range findings.ProfileProblems {
		if p.Entry == "gone" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no warning for the undeclared entry: %+v", findings.ProfileProblems)
	}

	after, err := os.ReadFile(profile.Path(namespaceDir))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("the profile manifest was rewritten:\n%s", after)
	}
}

func TestRun_UndeclaredProfileFolderAndOverride_AreWarned(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".config", "cfg")
	namespaceDir, payload := setupNamespace(t, dest)
	mustSymlink(t, payload, dest)

	if err := profile.Write(namespaceDir, profile.Manifest{}); err != nil {
		t.Fatal(err)
	}
	strayDir := profile.ProfileDir(namespaceDir, "dark")
	if err := os.MkdirAll(strayDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(strayDir, "cfg"), []byte("dark"), 0644); err != nil {
		t.Fatal(err)
	}

	findings, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var folder, override bool
	for _, p := range findings.ProfileProblems {
		if p.Profile == "dark" && p.Entry == "" {
			folder = true
		}
		if p.Profile == "dark" && p.Entry == "cfg" {
			override = true
		}
	}
	if !folder || !override {
		t.Fatalf("expected both warnings, got %+v", findings.ProfileProblems)
	}
	if readLink(t, dest) != payload {
		t.Fatal("an undeclared override was linked")
	}
}

// A declared profile with no folder, and a declared entry no profile
// overrides, are both normal: nothing is reported.
func TestRun_EmptyProfileAndUnoverriddenEntry_AreSilent(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".config", "cfg")
	namespaceDir, payload := setupNamespace(t, dest)
	mustSymlink(t, payload, dest)

	if err := profile.Write(namespaceDir, profile.Manifest{Profiles: []string{"dark"}, Entries: []string{"cfg"}}); err != nil {
		t.Fatal(err)
	}

	findings, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(findings.ProfileProblems) != 0 {
		t.Fatalf("expected silence, got %+v", findings.ProfileProblems)
	}
}

// A profile deleted on another machine and synced here leaves this machine
// with an active profile nothing declares. The namespace falls back to main
// and the relink is reported, since it changes what sits at a destination.
func TestRun_ActiveProfileNoLongerDeclared_FallsBackToMain(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(home, ".config", "cfg")
	namespaceDir, payload := setupNamespace(t, dest)

	if err := profile.Write(namespaceDir, profile.Manifest{Entries: []string{"cfg"}}); err != nil {
		t.Fatal(err)
	}
	override := filepath.Join(profile.ProfileDir(namespaceDir, "dark"), "cfg")
	if err := os.MkdirAll(filepath.Dir(override), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(override, []byte("dark"), 0644); err != nil {
		t.Fatal(err)
	}
	mustSymlink(t, override, dest)
	setActiveProfile(t, "dark", dest)

	findings, err := Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(findings.ProfileFallbacks) != 1 {
		t.Fatalf("fallbacks = %+v", findings.ProfileFallbacks)
	}
	fallback := findings.ProfileFallbacks[0]
	if fallback.Profile != "dark" || len(fallback.Relinked) != 1 || fallback.Relinked[0] != dest {
		t.Fatalf("fallback = %+v", fallback)
	}
	if readLink(t, dest) != payload {
		t.Fatalf("destination still points at %q", readLink(t, dest))
	}

	s, err := state.Read()
	if err != nil {
		t.Fatal(err)
	}
	if s.Entries[state.Key{Repo: "dotfiles", Namespace: "cfg-ns"}].ActiveProfile != "" {
		t.Fatal("state still records the deleted profile as active")
	}
}
