package commands

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/paths"
	"github.com/DeprecatedLuar/dotz/internal/selfheal"
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

// TestHandleList_ShowsSelfHealAutoDisabledNamespaceAsMaterialized covers the
// hand-off between self-heal and listing within one invocation: a namespace
// self-heal cannot fully link is flipped to disabled and reported, per
// concept.md "Self-healing", and the listing that follows shows the
// resulting "-", never a mute "!".
func TestHandleList_ShowsSelfHealAutoDisabledNamespaceAsMaterialized(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
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

	nsDir := filepath.Join(repoDir, "akeyshually")
	if err := os.MkdirAll(nsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nsDir, "akeyshually"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	dest := filepath.Join(home, ".config", "akeyshually")
	if err := manifest.Write(nsDir, manifest.Manifest{Entries: []manifest.Entry{{Name: "akeyshually", Dest: dest}}}); err != nil {
		t.Fatal(err)
	}
	// The destination is occupied by a real directory, as if some other
	// tool recreated it — the exact shape self-heal cannot fix.
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(state.State{Entries: map[state.Key]state.Entry{
		{Repo: "dotfiles", Namespace: "akeyshually"}: {Enabled: true},
	}}); err != nil {
		t.Fatal(err)
	}

	findings, err := selfheal.Run()
	if err != nil {
		t.Fatalf("selfheal.Run: %v", err)
	}
	if len(findings.Disabled) != 1 || findings.Disabled[0].Namespace != "akeyshually" {
		t.Fatalf("expected akeyshually to be auto-disabled, got %+v", findings.Disabled)
	}

	stdout, stderr := captureStdoutStderr(t, func() {
		if err := HandleList(nil); err != nil {
			t.Fatalf("HandleList: %v", err)
		}
	})
	if stderr != "" {
		t.Fatalf("unexpected stderr: %q", stderr)
	}
	if stdout != "- akeyshually\n" {
		t.Fatalf("expected the auto-disabled namespace to list as materialized, got %q", stdout)
	}
}

// TestHandleList_IgnoredNamespaceProducesNoRow covers concept.md
// "Namespace": a namespace whose manifest carries ignore = true is invisible
// to listing, the same as a folder with no .dots at all.
func TestHandleList_IgnoredNamespaceProducesNoRow(t *testing.T) {
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
	for _, name := range []string{"nixos", "scratch"} {
		nsDir := filepath.Join(repoDir, name)
		if err := os.MkdirAll(nsDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := manifest.Write(nsDir, manifest.Manifest{Ignore: name == "scratch"}); err != nil {
			t.Fatal(err)
		}
	}

	stdout, stderr := captureStdoutStderr(t, func() {
		if err := HandleList(nil); err != nil {
			t.Fatalf("HandleList: %v", err)
		}
	})
	if stderr != "" {
		t.Fatalf("unexpected stderr: %q", stderr)
	}
	if stdout != "- nixos\n" {
		t.Fatalf("expected ignored namespace to be invisible, got %q", stdout)
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

// TestHandleList_StatusAliasIsByteIdentical proves `dots` and `dots status`
// produce byte-identical output: both route to HandleList with the same
// argument shape (cmd/dotz's resolveRoute maps "status" to targetList, same
// as bare invocation), so calling it twice with the request's own args must
// come back identical.
func TestHandleList_StatusAliasIsByteIdentical(t *testing.T) {
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
	nsDir := filepath.Join(repoDir, "nixos")
	if err := os.MkdirAll(nsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Write(nsDir, manifest.Manifest{}); err != nil {
		t.Fatal(err)
	}

	bareOut, bareErr := captureStdoutStderr(t, func() {
		if err := HandleList(nil); err != nil {
			t.Fatalf("HandleList(nil): %v", err)
		}
	})
	// cmd/dotz's resolveRoute passes args[1:] of ["status"] through, an
	// empty (non-nil) slice — HandleList treats it the same as nil, which
	// is exactly the byte-identical guarantee under test.
	statusOut, statusErr := captureStdoutStderr(t, func() {
		if err := HandleList([]string{}); err != nil {
			t.Fatalf("HandleList([]string{}): %v", err)
		}
	})

	if bareOut != statusOut {
		t.Fatalf("stdout differs: bare=%q status=%q", bareOut, statusOut)
	}
	if bareErr != statusErr {
		t.Fatalf("stderr differs: bare=%q status=%q", bareErr, statusErr)
	}
}

// TestClassifyEntry_EmptyDestinationMarksUntracked covers: a manifest entry
// with an empty destination marks the entry "?" and is never enabled —
// concept.md "Manual edits": invalid, not pending.
func TestClassifyEntry_EmptyDestinationMarksUntracked(t *testing.T) {
	invalid := map[string]bool{"cfg": true}
	row := classifyEntry(manifest.Entry{Name: "cfg", Dest: ""}, t.TempDir(), true, "", invalid, nil)
	if row.Marker != ui.MarkerUntracked {
		t.Fatalf("expected marker %q, got %q", ui.MarkerUntracked, row.Marker)
	}
}

// TestClassifyEntry_MissingPayloadIsOrphan covers the orphan case: a
// tracked entry whose payload is gone from an installed namespace is
// marked "!".
func TestClassifyEntry_MissingPayloadIsOrphan(t *testing.T) {
	nsDir := t.TempDir()
	orphaned := map[string]bool{"gone": true}
	row := classifyEntry(manifest.Entry{Name: "gone", Dest: "/tmp/whatever"}, nsDir, true, "", nil, orphaned)
	if row.Marker != ui.MarkerProblem {
		t.Fatalf("expected marker %q, got %q", ui.MarkerProblem, row.Marker)
	}
}

// TestEntryListing_UntrackedPayloadMarked covers: a payload present in the
// namespace but absent from the manifest is marked "?".
func TestEntryListing_UntrackedPayloadMarked(t *testing.T) {
	nsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(nsDir, "mystery"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	rows, _, err := entryListing(nsDir, nil, false, "")
	if err != nil {
		t.Fatalf("entryListing: %v", err)
	}
	if len(rows) != 1 || rows[0].Marker != ui.MarkerUntracked || rows[0].Name != "mystery" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
}

// TestEntryListing_OrphanAndUntrackedSuggestRename covers the scope's
// rename suggestion: an orphan and an untracked file in the same namespace
// are almost certainly a rename, and entryListing says so.
func TestEntryListing_OrphanAndUntrackedSuggestRename(t *testing.T) {
	nsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(nsDir, "newname"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	entries := []manifest.Entry{{Name: "oldname", Dest: "/tmp/oldname-dest"}}

	rows, suggestion, err := entryListing(nsDir, entries, false, "")
	if err != nil {
		t.Fatalf("entryListing: %v", err)
	}
	if suggestion == "" {
		t.Fatalf("expected a rename suggestion, got none; rows=%+v", rows)
	}
	if !strings.Contains(suggestion, "oldname") || !strings.Contains(suggestion, "newname") {
		t.Fatalf("suggestion doesn't name both sides: %q", suggestion)
	}
}

// TestNamespaceRow_PromotesToProblemOnOrphanEntry covers: a namespace with
// an orphaned entry underneath it is reported "!" at the top level, per
// concept.md "a `!` namespace always has at least one `!` or `?` entry
// underneath it".
func TestNamespaceRow_PromotesToProblemOnOrphanEntry(t *testing.T) {
	nsDir := t.TempDir()
	entries := []manifest.Entry{{Name: "gone", Dest: "/tmp/gone-dest"}}
	s := state.State{Entries: map[state.Key]state.Entry{}}

	row, err := namespaceRow(s, "dotfiles", "cfg-ns", nsDir, entries)
	if err != nil {
		t.Fatalf("namespaceRow: %v", err)
	}
	if row.Marker != ui.MarkerProblem {
		t.Fatalf("expected marker %q, got %q", ui.MarkerProblem, row.Marker)
	}
}

// TestNamespaceRow_HealthyEnabledNamespaceStaysPlus proves the "!" rollup
// doesn't fire for an ordinary correctly-linked namespace.
func TestNamespaceRow_HealthyEnabledNamespaceStaysPlus(t *testing.T) {
	nsDir := t.TempDir()
	payload := filepath.Join(nsDir, "cfg")
	if err := os.WriteFile(payload, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "cfg-dest")
	if err := os.Symlink(payload, dest); err != nil {
		t.Fatal(err)
	}
	entries := []manifest.Entry{{Name: "cfg", Dest: dest}}
	s := state.State{Entries: map[state.Key]state.Entry{
		{Repo: "dotfiles", Namespace: "cfg-ns"}: {Enabled: true},
	}}

	row, err := namespaceRow(s, "dotfiles", "cfg-ns", nsDir, entries)
	if err != nil {
		t.Fatalf("namespaceRow: %v", err)
	}
	if row.Marker != ui.MarkerEnabled {
		t.Fatalf("expected marker %q, got %q", ui.MarkerEnabled, row.Marker)
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
