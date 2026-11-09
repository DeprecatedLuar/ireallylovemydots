package git

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCleanOutput_DropsStockLinesAndContractsHome(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	in := "Bad owner or permissions on /home/u/.ssh/config.d/lsh\r\n" +
		"fatal: Could not read from remote repository.\n\n" +
		"Please make sure you have the correct access rights\n" +
		"and the repository exists.\n"
	if got := cleanOutput(in); got != "Bad owner or permissions on ~/.ssh/config.d/lsh" {
		t.Fatalf("cleanOutput = %q", got)
	}
}

func TestRemoteNotes_KeepsOnlyServerLinesTrimmed(t *testing.T) {
	in := "To example:repo\nremote: This repository moved        \n   abc..def  main -> main\nremote:    \n"
	got := remoteNotes(in)
	if len(got) != 1 || got[0] != "remote: This repository moved" {
		t.Fatalf("remoteNotes = %q", got)
	}
}

func TestWithPromptsOff_AddsOnlyWhatIsUnset(t *testing.T) {
	got := withPromptsOff([]string{"PATH=/bin"})
	for _, want := range []string{"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never"} {
		if !slices.Contains(got, want) {
			t.Fatalf("env %v missing %s", got, want)
		}
	}
}

func TestWithPromptsOff_KeepsUserSetting(t *testing.T) {
	got := withPromptsOff([]string{"GIT_TERMINAL_PROMPT=1"})
	if slices.Contains(got, "GIT_TERMINAL_PROMPT=0") {
		t.Fatalf("env %v overrode the user's GIT_TERMINAL_PROMPT", got)
	}
}

func TestPrepare_FetchFailureIsNetErrorWithCleanOutput(t *testing.T) {
	_, _, second := newReconcileClones(t)
	gitRun(t, second, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git"))
	_, err := Prepare(second, nil, nil, false)
	var ne *NetError
	if !errors.As(err, &ne) || ne.Step != StepFetch {
		t.Fatalf("err = %v, want a NetError for the fetch", err)
	}
	if strings.Contains(ne.Output, "Please make sure") || ne.Output == "" {
		t.Fatalf("output = %q, want git's cause without stock lines", ne.Output)
	}
}

func TestPrepare_FetchesWithoutListingTheRemote(t *testing.T) {
	_, first, second := newReconcileClones(t)
	pushEdit(t, first, "ns/file", "remote")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = \"ls-remote\" ]; then echo 'ls-remote called' >&2; exit 1; fi\nexec " + realGit + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	p, err := Prepare(second, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Remote == "" {
		t.Fatal("expected the remote head")
	}
}

func TestPrepare_EmptyRemoteHasNoRemoteHead(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	local := filepath.Join(root, "local")
	gitRun(t, root, "init", "--bare", "-b", "main", remote)
	gitRun(t, root, "init", "-b", "main", local)
	configureReconcileRepo(t, local)
	gitRun(t, local, "remote", "add", "origin", remote)
	writeReconcileFile(t, local, "ns/file", "local")

	p, err := Prepare(local, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Remote != "" {
		t.Fatalf("Remote = %q, want none", p.Remote)
	}
}

func TestPush_ReturnsServerNotes(t *testing.T) {
	remote, _, second := newReconcileClones(t)
	hook := filepath.Join(remote, "hooks", "post-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho 'This repository moved' >&2\n"), 0755); err != nil {
		t.Fatal(err)
	}
	writeReconcileFile(t, second, "ns/file", "local")
	gitRun(t, second, "add", "-A")
	gitRun(t, second, "commit", "-m", "edit")

	notes, err := Push(second, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0] != "remote: This repository moved" {
		t.Fatalf("notes = %q", notes)
	}
}
