package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/profile"
	"github.com/DeprecatedLuar/dotz/internal/state"
)

// registerNamespaceWithFiles registers one repository holding one namespace
// whose entries are files (registerRepoWithNamespace creates directories),
// since a profile override is most readable as file content.
func registerNamespaceWithFiles(t *testing.T, nsName string, names []string) (nsDir string, entries []manifest.Entry) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	home := t.TempDir()

	reg := manifest.Registry{Repos: []manifest.Repo{
		{Name: "dotfiles", Owner: "someone", URL: "https://example.com/someone/dotfiles"},
	}}
	if err := manifest.WriteRegistry(reg); err != nil {
		t.Fatal(err)
	}

	nsDir = filepath.Join(dataHome, "ireallylovemydots", "dotfiles", nsName)
	if err := os.MkdirAll(nsDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(nsDir, name), []byte("root "+name), 0644); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, manifest.Entry{Name: name, Dest: filepath.Join(home, name)})
	}
	if err := manifest.Write(nsDir, manifest.Manifest{Entries: entries}); err != nil {
		t.Fatal(err)
	}
	return nsDir, entries
}

func enableForProfiles(t *testing.T, nsName string) {
	t.Helper()
	if err := enableNamespace(nsName, shared.Flags{}); err != nil {
		t.Fatalf("enable %s: %v", nsName, err)
	}
}

func activeProfile(t *testing.T, nsName string) string {
	t.Helper()
	s, err := state.Read()
	if err != nil {
		t.Fatal(err)
	}
	return s.Entries[state.Key{Repo: "dotfiles", Namespace: nsName}].ActiveProfile
}

func TestProfilesAddOverride_UndeclaredEntry_NonInteractiveNamesMainAdd(t *testing.T) {
	nsDir, _ := registerNamespaceWithFiles(t, "editors", []string{"gitconfig"})
	if err := profile.Create(nsDir, "dark", ""); err != nil {
		t.Fatal(err)
	}

	err := handleProfiles("editors", []string{"dark", "add", "gitconfig"}, shared.Flags{})
	if err == nil {
		t.Fatal("expected an error for an undeclared entry")
	}
	if !strings.Contains(err.Error(), "profiles main add gitconfig") {
		t.Fatalf("error does not name the declaring command: %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(profile.ProfileDir(nsDir, "dark"), "gitconfig")); !os.IsNotExist(statErr) {
		t.Fatal("an override was created despite the error")
	}
}

func TestProfilesAddOverride_UndeclaredEntry_ForceDeclaresIt(t *testing.T) {
	nsDir, _ := registerNamespaceWithFiles(t, "editors", []string{"gitconfig"})
	if err := profile.Create(nsDir, "dark", ""); err != nil {
		t.Fatal(err)
	}

	captureStdoutStderr(t, func() {
		if err := handleProfiles("editors", []string{"dark", "add", "gitconfig"}, shared.Flags{Force: true, Yes: true}); err != nil {
			t.Fatalf("forced add: %v", err)
		}
	})

	pm, err := profile.Read(nsDir)
	if err != nil {
		t.Fatal(err)
	}
	if !pm.HasEntry("gitconfig") {
		t.Fatal("--force did not declare the entry")
	}
	if _, err := os.Lstat(filepath.Join(profile.ProfileDir(nsDir, "dark"), "gitconfig")); err != nil {
		t.Fatalf("override was not created: %v", err)
	}
}

func TestProfilesRm_ActiveProfileReturnsToMainAndReportsRelinks(t *testing.T) {
	nsDir, entries := registerNamespaceWithFiles(t, "editors", []string{"gitconfig", "plain"})
	enableForProfiles(t, "editors")
	if err := profile.Write(nsDir, profile.Manifest{}); err != nil {
		t.Fatal(err)
	}
	captureStdoutStderr(t, func() {
		if err := handleProfiles("editors", []string{"main", "add", "gitconfig"}, shared.Flags{}); err != nil {
			t.Fatal(err)
		}
		if err := handleProfiles("editors", []string{"add", "dark"}, shared.Flags{}); err != nil {
			t.Fatal(err)
		}
		if err := handleProfiles("editors", []string{"dark", "add", "gitconfig"}, shared.Flags{}); err != nil {
			t.Fatal(err)
		}
		if err := handleProfiles("editors", []string{"dark", "enable"}, shared.Flags{}); err != nil {
			t.Fatal(err)
		}
	})
	if activeProfile(t, "editors") != "dark" {
		t.Fatal("dark did not become active")
	}

	var stdout string
	stdout, _ = captureStdoutStderr(t, func() {
		if err := handleProfiles("editors", []string{"rm", "dark"}, shared.Flags{}); err != nil {
			t.Fatalf("profiles rm: %v", err)
		}
	})

	if activeProfile(t, "editors") != "" {
		t.Fatalf("active profile = %q, want main", activeProfile(t, "editors"))
	}
	if !strings.Contains(stdout, entries[0].Dest) {
		t.Fatalf("report does not name the relinked destination:\n%s", stdout)
	}
	target, err := os.Readlink(entries[0].Dest)
	if err != nil {
		t.Fatal(err)
	}
	if target != filepath.Join(nsDir, "gitconfig") {
		t.Fatalf("destination target = %q, want the root copy", target)
	}
}

// Every profile operation writes only committed artifacts — the profile
// manifest and the profile folders. Which profile is active is machine
// state, and stays out of the repository entirely.
func TestProfileOperations_WriteNothingMachineSpecificIntoTheRepository(t *testing.T) {
	nsDir, _ := registerNamespaceWithFiles(t, "editors", []string{"gitconfig"})
	enableForProfiles(t, "editors")
	if err := profile.Write(nsDir, profile.Manifest{}); err != nil {
		t.Fatal(err)
	}
	captureStdoutStderr(t, func() {
		for _, args := range [][]string{
			{"main", "add", "gitconfig"},
			{"add", "dark"},
			{"dark", "add", "gitconfig"},
			{"dark", "enable"},
		} {
			if err := handleProfiles("editors", args, shared.Flags{}); err != nil {
				t.Fatalf("profiles %v: %v", args, err)
			}
		}
	})

	repoDir := filepath.Dir(nsDir)
	err := filepath.WalkDir(repoDir, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, "state.json") {
			t.Fatalf("machine state was written into the repository at %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(profile.Path(nsDir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "active") {
		t.Fatalf("the profile manifest records the active profile:\n%s", data)
	}
}

func TestProfilesMainRm_WithOverridesOutstanding_NamesTheProfileCommands(t *testing.T) {
	nsDir, _ := registerNamespaceWithFiles(t, "editors", []string{"gitconfig"})
	if err := profile.Write(nsDir, profile.Manifest{}); err != nil {
		t.Fatal(err)
	}
	captureStdoutStderr(t, func() {
		for _, args := range [][]string{
			{"main", "add", "gitconfig"},
			{"add", "dark"},
			{"dark", "add", "gitconfig"},
		} {
			if err := handleProfiles("editors", args, shared.Flags{}); err != nil {
				t.Fatalf("profiles %v: %v", args, err)
			}
		}
	})

	err := handleProfiles("editors", []string{"main", "rm", "gitconfig"}, shared.Flags{})
	if err == nil {
		t.Fatal("expected an error while dark still overrides the entry")
	}
	if !strings.Contains(err.Error(), "profiles dark rm gitconfig") {
		t.Fatalf("error does not name the clearing command: %v", err)
	}

	captureStdoutStderr(t, func() {
		if err := handleProfiles("editors", []string{"main", "rm", "gitconfig"}, shared.Flags{Force: true, Yes: true}); err != nil {
			t.Fatalf("forced main rm: %v", err)
		}
	})
	pm, err := profile.Read(nsDir)
	if err != nil {
		t.Fatal(err)
	}
	if pm.HasEntry("gitconfig") {
		t.Fatal("--force did not undeclare the entry")
	}
	if _, statErr := os.Lstat(filepath.Join(profile.ProfileDir(nsDir, "dark"), "gitconfig")); !os.IsNotExist(statErr) {
		t.Fatal("--force did not drop the override")
	}
}

func TestRmEntry_ProfiledEntry_NamesMainRm(t *testing.T) {
	nsDir, entries := registerNamespaceWithFiles(t, "editors", []string{"gitconfig"})
	if err := profile.Write(nsDir, profile.Manifest{}); err != nil {
		t.Fatal(err)
	}
	captureStdoutStderr(t, func() {
		if err := handleProfiles("editors", []string{"main", "add", "gitconfig"}, shared.Flags{}); err != nil {
			t.Fatal(err)
		}
	})

	err := rmEntry("editors", []string{entries[0].Name}, shared.Flags{Yes: true})
	if err == nil {
		t.Fatal("expected an error for a profiled entry")
	}
	if !strings.Contains(err.Error(), "profiles main rm gitconfig") {
		t.Fatalf("error does not name the undeclaring command: %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(nsDir, "gitconfig")); statErr != nil {
		t.Fatalf("the payload was removed despite the error: %v", statErr)
	}
}

func TestProfilesEnable_ProfileOverridingNothing_Warns(t *testing.T) {
	nsDir, entries := registerNamespaceWithFiles(t, "editors", []string{"gitconfig"})
	enableForProfiles(t, "editors")
	if err := profile.Create(nsDir, "bare", ""); err != nil {
		t.Fatal(err)
	}

	_, stderr := captureStdoutStderr(t, func() {
		if err := handleProfiles("editors", []string{"bare", "enable"}, shared.Flags{}); err != nil {
			t.Fatalf("enable bare: %v", err)
		}
	})
	if !strings.Contains(stderr, "overrides nothing") {
		t.Fatalf("no warning for an empty profile:\n%s", stderr)
	}

	target, err := os.Readlink(entries[0].Dest)
	if err != nil {
		t.Fatal(err)
	}
	if target != filepath.Join(nsDir, "gitconfig") {
		t.Fatalf("target = %q, want the root copy", target)
	}
}

func TestProfilesAdd_MainIsReserved(t *testing.T) {
	registerNamespaceWithFiles(t, "editors", []string{"gitconfig"})
	err := handleProfiles("editors", []string{"add", "main"}, shared.Flags{})
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("expected main to be refused as a profile name, got %v", err)
	}
}

func TestProfilesMv_ActiveProfile_RepointsDestinations(t *testing.T) {
	nsDir, entries := registerNamespaceWithFiles(t, "editors", []string{"gitconfig"})
	enableForProfiles(t, "editors")
	if err := profile.Write(nsDir, profile.Manifest{}); err != nil {
		t.Fatal(err)
	}
	captureStdoutStderr(t, func() {
		for _, args := range [][]string{
			{"main", "add", "gitconfig"},
			{"add", "dark"},
			{"dark", "add", "gitconfig"},
			{"dark", "enable"},
			{"mv", "dark", "night"},
		} {
			if err := handleProfiles("editors", args, shared.Flags{}); err != nil {
				t.Fatalf("profiles %v: %v", args, err)
			}
		}
	})

	if activeProfile(t, "editors") != "night" {
		t.Fatalf("active profile = %q", activeProfile(t, "editors"))
	}
	target, err := os.Readlink(entries[0].Dest)
	if err != nil {
		t.Fatal(err)
	}
	if target != filepath.Join(profile.ProfileDir(nsDir, "night"), "gitconfig") {
		t.Fatalf("target = %q, want the renamed profile's copy", target)
	}
}
