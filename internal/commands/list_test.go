package commands

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/paths"
	"github.com/DeprecatedLuar/dotz/internal/state"
	"github.com/DeprecatedLuar/dotz/internal/ui"
)

func captureStdoutStderr(t *testing.T, f func()) (stdout, stderr string) {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origOut, origErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	defer func() { os.Stdout, os.Stderr = origOut, origErr }()

	f()

	outW.Close()
	errW.Close()
	outBytes, _ := io.ReadAll(outR)
	errBytes, _ := io.ReadAll(errR)
	return string(outBytes), string(errBytes)
}

func TestHandleList_EmptyRegistryPrintsHintOnStderrOnly(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	stdout, stderr := captureStdoutStderr(t, func() {
		if err := HandleList(nil); err != nil {
			t.Fatalf("HandleList: %v", err)
		}
	})
	if stdout != "" {
		t.Fatalf("expected empty stdout, got %q", stdout)
	}
	if stderr != emptyRegistryHint+"\n" {
		t.Fatalf("expected hint on stderr, got %q", stderr)
	}
}

func TestHandleList_HintDisappearsOnceRepoRegistered(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)

	reg := manifest.Registry{Repos: []manifest.Repo{
		{Name: "dotfiles", Owner: "someone", URL: "https://example.com/someone/dotfiles"},
	}}
	if err := manifest.WriteRegistry(reg); err != nil {
		t.Fatalf("WriteRegistry: %v", err)
	}
	repoDir := filepath.Join(dataHome, "ireallylovemydots", "dotfiles")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatalf("create repository directory: %v", err)
	}
	if out, err := exec.Command("git", "init", repoDir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	_, stderr := captureStdoutStderr(t, func() {
		if err := HandleList(nil); err != nil {
			t.Fatalf("HandleList: %v", err)
		}
	})
	if stderr != "" {
		t.Fatalf("expected no hint once a repository is registered, got %q", stderr)
	}
}

func TestHandleList_MarksEnabledNamespacesWithPlus(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	reg := manifest.Registry{Repos: []manifest.Repo{{Name: "dotfiles"}}}
	if err := manifest.WriteRegistry(reg); err != nil {
		t.Fatal(err)
	}
	dataDir, err := paths.Data()
	if err != nil {
		t.Fatal(err)
	}
	repoDir := filepath.Join(dataDir, "dotfiles")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", repoDir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	for _, name := range []string{"nixos", "starship"} {
		nsDir := filepath.Join(repoDir, name)
		if err := os.MkdirAll(nsDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := manifest.Write(nsDir, manifest.Manifest{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.Write(state.State{Entries: map[state.Key]state.Entry{
		{Repo: "dotfiles", Namespace: "nixos"}: {Enabled: true},
	}}); err != nil {
		t.Fatal(err)
	}

	stdout, stderr := captureStdoutStderr(t, func() {
		if err := HandleList(nil); err != nil {
			t.Fatalf("HandleList: %v", err)
		}
	})
	if stderr != "" {
		t.Fatalf("unexpected stderr: %q", stderr)
	}
	if stdout != "+ nixos\n- starship\n" {
		t.Fatalf("unexpected listing: %q", stdout)
	}
}

func TestSortNamespaceRows_GroupsByState(t *testing.T) {
	entries := []ui.Entry{
		{Marker: ui.MarkerMaterialized, Name: "z-disabled"},
		{Marker: ui.MarkerEnabled, Name: "z-enabled"},
		{Marker: ui.MarkerAbsent, Name: "available"},
		{Marker: ui.MarkerProblem, Name: "broken"},
		{Marker: ui.MarkerProblem, Name: "another-broken"},
	}

	sortNamespaceRows(entries)

	got := make([]string, len(entries))
	for i, entry := range entries {
		got[i] = entry.Marker + " " + entry.Name
	}
	want := []string{
		"! another-broken",
		"! broken",
		"+ z-enabled",
		"- z-disabled",
		"= available",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sorted entries = %v, want %v", got, want)
	}
}

// TestRenderRepoNamespaces_MatchesUnscopedMarkers is the regression guard for
// the bug that motivated centralizing listing: `repo <name> ls` used to
// hardcode "=" for every namespace regardless of its real state. It must now
// report the same markers the unscoped listing does, narrowed to this repo.
func TestRenderRepoNamespaces_MatchesUnscopedMarkers(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	reg := manifest.Registry{Repos: []manifest.Repo{{Name: "dotfiles"}}}
	if err := manifest.WriteRegistry(reg); err != nil {
		t.Fatal(err)
	}
	dataDir, err := paths.Data()
	if err != nil {
		t.Fatal(err)
	}
	repoDir := filepath.Join(dataDir, "dotfiles")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", repoDir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	for _, name := range []string{"nixos", "starship"} {
		nsDir := filepath.Join(repoDir, name)
		if err := os.MkdirAll(nsDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := manifest.Write(nsDir, manifest.Manifest{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.Write(state.State{Entries: map[state.Key]state.Entry{
		{Repo: "dotfiles", Namespace: "nixos"}: {Enabled: true},
	}}); err != nil {
		t.Fatal(err)
	}

	stdout, stderr := captureStdoutStderr(t, func() {
		if err := HandleRepo([]string{"dotfiles", "list"}, shared.Flags{}); err != nil {
			t.Fatalf("HandleRepo: %v", err)
		}
	})
	if stderr != "" {
		t.Fatalf("unexpected stderr: %q", stderr)
	}
	if stdout != "+ nixos\n- starship\n" {
		t.Fatalf("unexpected listing: %q", stdout)
	}
}

// TestNamespaceListing_StateFilterExcludesAbsent proves opts.States narrows
// namespaceListing's result: a namespace only in the catalogue ("=") is
// excluded when the caller asks for on-disk states only.
func TestNamespaceListing_StateFilterExcludesAbsent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	reg := manifest.Registry{Repos: []manifest.Repo{{Name: "dotfiles"}}}
	dataDir, err := paths.Data()
	if err != nil {
		t.Fatal(err)
	}
	repoDir := filepath.Join(dataDir, "dotfiles")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", repoDir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	nsDir := filepath.Join(repoDir, "nixos")
	if err := os.MkdirAll(nsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Write(nsDir, manifest.Manifest{}); err != nil {
		t.Fatal(err)
	}

	rows, err := namespaceListing(reg.Repos, listOptions{States: []string{ui.MarkerEnabled, ui.MarkerMaterialized}})
	if err != nil {
		t.Fatalf("namespaceListing: %v", err)
	}
	if len(rows) != 1 || rows[0].Marker != ui.MarkerMaterialized || rows[0].Name != "nixos" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
}
