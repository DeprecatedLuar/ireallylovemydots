package commands

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeprecatedLuar/ireallylovemydots/internal/commands/shared"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/git"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/state"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/ui"
)

func TestRenderSyncResult_PlainWhenNotATerminal(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	cases := []struct {
		name string
		res  syncResult
		want string
	}{
		{"clean", syncResult{name: "krita-config"}, "✓ krita-config\n"},
		{"removed upstream", syncResult{name: "dots", nodes: []ui.TreeNode{{Name: "luxos", Marker: ui.MarkerRemovedUpstream}}},
			"✓ dots\n  └── ⊘ luxos\n"},
		{"conflict", syncResult{name: "luxos-dots", step: stepConflict, nodes: []ui.TreeNode{
			{Name: "luxos", LeafMarker: ui.MarkerProblem, Paths: []string{"flake.lock"}}}},
			"✗ luxos-dots (conflict)\n  └── luxos\n      └── ! flake.lock\n"},
		{"fetch failed", syncResult{name: "private-dots", step: git.StepFetch,
			err: &git.NetError{Step: git.StepFetch, Output: "Bad owner or permissions on ~/.ssh/config.d/lsh"}},
			"✗ private-dots (fetch failed)\n  Bad owner or permissions on ~/.ssh/config.d/lsh\n"},
		{"other failure", syncResult{name: "repo", err: errors.New("removal declined; nothing applied")},
			"✗ repo\n  removal declined; nothing applied\n"},
		{"remote note", syncResult{name: "repo", notes: []string{"remote: This repository moved"}},
			"✓ repo\n  remote: This repository moved\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderSyncResult(tc.res); got != tc.want {
				t.Fatalf("renderSyncResult =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

func TestConflictTip(t *testing.T) {
	if got := conflictTip([]syncResult{{name: "a"}}); got != "" {
		t.Fatalf("tip with no conflict = %q, want none", got)
	}
	one := []syncResult{{name: "r", held: []string{"luxos"}}}
	if got := conflictTip(one); got != "tip: dots sync luxos --overwrite-remote | --overwrite-local" {
		t.Fatalf("tip = %q", got)
	}
	two := []syncResult{{name: "r", held: []string{"luxos"}}, {name: "s", held: []string{"kitty"}}}
	if got := conflictTip(two); got != "tip: dots sync <namespace> --overwrite-remote | --overwrite-local" {
		t.Fatalf("tip = %q", got)
	}
}

func TestHandleSync_FetchFailureNamesTheStep(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	repoDir, _ := newRegisteredRepo(t, dataDir, scratchRoot, "repo", []string{"a"})
	syncGitRun(t, repoDir, "remote", "set-url", "origin", filepath.Join(scratchRoot, "missing.git"))

	var err error
	stdout, _ := captureStdoutStderr(t, func() { err = HandleSync(nil, shared.Flags{}) })
	if !errors.Is(err, ErrSomeSkipped) {
		t.Fatalf("err = %v, want ErrSomeSkipped", err)
	}
	if !strings.Contains(stdout, "✗ repo (fetch failed)") || strings.Contains(stdout, "Please make sure") {
		t.Fatalf("stdout:\n%s\nwant a fetch-failed line without git's stock lines", stdout)
	}
}

func TestHandleSync_PushFailureNamesTheStep(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	repoDir, _ := newRegisteredRepo(t, dataDir, scratchRoot, "repo", []string{"a"})
	writeTestFile(t, filepath.Join(repoDir, "a", "file"), "local")
	installFakeGitPushAuthFailure(t)

	var err error
	stdout, _ := captureStdoutStderr(t, func() { err = HandleSync(nil, shared.Flags{}) })
	if !errors.Is(err, ErrSomeSkipped) || !strings.Contains(stdout, "✗ repo (push failed)") {
		t.Fatalf("err = %v, stdout:\n%s\nwant a push-failed line", err, stdout)
	}
}

func TestHandleSync_LeftAsIsIsANodeNotAFailure(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	repoDir, _ := newRegisteredRepo(t, dataDir, scratchRoot, "repo", []string{"a", "b"})
	writeTestFile(t, filepath.Join(repoDir, "b", "file"), "local b")

	var err error
	stdout, _ := captureStdoutStderr(t, func() { err = HandleSync([]string{"a"}, shared.Flags{}) })
	if err != nil {
		t.Fatalf("HandleSync: %v", err)
	}
	if !strings.Contains(stdout, "✓ repo\n  └── b\n") {
		t.Fatalf("stdout:\n%s\nwant b as a left-as-is node", stdout)
	}
}

func TestHandleSync_ShowsServerNotesAfterPush(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	repoDir, remote := newRegisteredRepo(t, dataDir, scratchRoot, "repo", []string{"a"})
	hook := filepath.Join(remote, "hooks", "post-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho 'This repository moved' >&2\n"), 0755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(repoDir, "a", "file"), "local")

	var err error
	stdout, _ := captureStdoutStderr(t, func() { err = HandleSync(nil, shared.Flags{}) })
	if err != nil {
		t.Fatalf("HandleSync: %v", err)
	}
	if !strings.Contains(stdout, "remote: This repository moved") {
		t.Fatalf("stdout:\n%s\nwant the server's note", stdout)
	}
}

func TestHandleSync_LooseRootConflictsFailTheRepositoryUntouched(t *testing.T) {
	dataDir, scratchRoot := setupSyncEnv(t)
	repoDir, remote := newRegisteredRepo(t, dataDir, scratchRoot, "repo", []string{"a"})
	writeTestFile(t, filepath.Join(repoDir, "Makefile"), "seed")
	writeTestFile(t, filepath.Join(repoDir, "README"), "seed")
	syncGitRun(t, repoDir, "add", "-A")
	syncGitRun(t, repoDir, "commit", "-m", "loose files")
	syncGitRun(t, repoDir, "push", "origin", "main")
	pushPeerEdit(t, scratchRoot, remote, "a/file", "peer a")
	pushPeerEdit(t, scratchRoot, remote, "Makefile", "peer make")
	pushPeerEdit(t, scratchRoot, remote, "README", "peer readme")
	writeTestFile(t, filepath.Join(repoDir, "a", "file"), "local a")
	writeTestFile(t, filepath.Join(repoDir, "Makefile"), "local make")
	writeTestFile(t, filepath.Join(repoDir, "README"), "local readme")

	var err error
	stdout, stderr := captureStdoutStderr(t, func() { err = HandleSync(nil, shared.Flags{}) })
	if err == nil {
		t.Fatal("HandleSync: want the repository to fail")
	}
	if want := "✗ repo (conflict)\n  ├── ! Makefile\n  └── ! README\n"; !strings.Contains(stdout, want) {
		t.Fatalf("stdout missing %q:\n%s", want, stdout)
	}
	if strings.Contains(stdout, "a\n") {
		t.Fatalf("stdout lists the namespace as held:\n%s", stdout)
	}
	if strings.Contains(stderr, "tip:") {
		t.Fatalf("stderr has a tip:\n%s", stderr)
	}
	if want := "resolve it by hand in the clone: " + repoDir; !strings.Contains(stdout, want) {
		t.Fatalf("stdout missing %q:\n%s", want, stdout)
	}
	for rel, want := range map[string]string{"a/file": "local a", "Makefile": "local make", "README": "local readme"} {
		if got := readFile(t, filepath.Join(repoDir, rel)); got != want {
			t.Fatalf("%s = %q, want untouched %q", rel, got, want)
		}
	}
	s, _ := state.Read()
	if s.Entries[state.Key{Repo: "repo", Namespace: "a"}].HeldBase != "" {
		t.Fatal("no held base should be recorded")
	}
}
