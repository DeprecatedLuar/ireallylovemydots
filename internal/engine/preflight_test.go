package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/paths"
	"github.com/DeprecatedLuar/dotz/internal/state"
)

func emptyState() state.State {
	return state.State{Entries: map[state.Key]state.Entry{}}
}

func TestPreflight_AbsentEmptyDirDanglingSymlink_NotOccupied(t *testing.T) {
	home := t.TempDir()
	nsDir := t.TempDir()

	absent := filepath.Join(home, "absent")
	emptyDir := filepath.Join(home, "empty")
	if err := os.MkdirAll(emptyDir, 0755); err != nil {
		t.Fatal(err)
	}
	danglingTarget := filepath.Join(home, "gone")
	dangling := filepath.Join(home, "dangling")
	if err := os.Symlink(danglingTarget, dangling); err != nil {
		t.Fatal(err)
	}

	entries := []manifest.Entry{
		{Name: "absent", Dest: absent},
		{Name: "empty", Dest: emptyDir},
		{Name: "dangling", Dest: dangling},
	}
	key := state.Key{Repo: "dotfiles", Namespace: "editors"}
	problems, err := Preflight(key, nsDir, entries, emptyState())
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("expected no problems for absent/empty-dir/dangling-symlink destinations, got %+v", problems)
	}
}

func TestPreflight_OccupiedRealFileAndNonEmptyDir(t *testing.T) {
	home := t.TempDir()
	nsDir := t.TempDir()

	realFile := filepath.Join(home, "config")
	if err := os.WriteFile(realFile, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	nonEmptyDir := filepath.Join(home, "ssh")
	if err := os.MkdirAll(nonEmptyDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nonEmptyDir, "id_rsa"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	entries := []manifest.Entry{
		{Name: "config", Dest: realFile},
		{Name: "ssh", Dest: nonEmptyDir},
	}
	key := state.Key{Repo: "dotfiles", Namespace: "editors"}
	problems, err := Preflight(key, nsDir, entries, emptyState())
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if len(problems) != 2 {
		t.Fatalf("expected 2 occupied problems, got %+v", problems)
	}
	for _, p := range problems {
		if p.Kind != Occupied {
			t.Fatalf("expected Occupied, got %+v", p)
		}
		if !strings.Contains(p.Message, "--force") || !strings.Contains(p.Message, "track the paths inside it") {
			t.Fatalf("expected occupied message to name both --force and tracking the paths inside it, got: %s", p.Message)
		}
	}
}

func TestPreflight_ProtectedRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	nsDir := t.TempDir()

	entries := []manifest.Entry{{Name: "home", Dest: home}}
	key := state.Key{Repo: "dotfiles", Namespace: "editors"}
	problems, err := Preflight(key, nsDir, entries, emptyState())
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if len(problems) != 1 || problems[0].Kind != ProtectedRoot {
		t.Fatalf("expected one ProtectedRoot problem, got %+v", problems)
	}
}

func TestPreflight_SelfContainmentGuard(t *testing.T) {
	home := t.TempDir()
	nsDir := t.TempDir()

	nvimDir := filepath.Join(home, ".config", "nvim")
	initLua := filepath.Join(nvimDir, "init.lua")

	entries := []manifest.Entry{
		{Name: "nvim", Dest: nvimDir},
		{Name: "init.lua", Dest: initLua},
	}
	key := state.Key{Repo: "dotfiles", Namespace: "editors"}
	problems, err := Preflight(key, nsDir, entries, emptyState())
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if len(problems) != 2 {
		t.Fatalf("expected both entries flagged by the in-repo link guard, got %+v", problems)
	}
	for _, p := range problems {
		if p.Kind != LinkGuard {
			t.Fatalf("expected LinkGuard, got %+v", p)
		}
	}
}

func TestPreflight_InsideDataDirGuard_ThroughExistingSymlink(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dataDir, err := paths.Data()
	if err != nil {
		t.Fatal(err)
	}
	nsDir := filepath.Join(dataDir, "dotfiles", "editors")
	if err := os.MkdirAll(nsDir, 0755); err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()

	payloadDir := filepath.Join(nsDir, "nvim")
	if err := os.MkdirAll(payloadDir, 0755); err != nil {
		t.Fatal(err)
	}
	nvimDest := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(filepath.Dir(nvimDest), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(payloadDir, nvimDest); err != nil {
		t.Fatal(err)
	}

	entries := []manifest.Entry{
		{Name: "init.lua", Dest: filepath.Join(nvimDest, "init.lua")},
	}
	key := state.Key{Repo: "dotfiles", Namespace: "other"}
	problems, err := Preflight(key, nsDir, entries, emptyState())
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if len(problems) != 1 || problems[0].Kind != LinkGuard {
		t.Fatalf("expected a LinkGuard problem for a destination resolving through an existing symlink into the data directory, got %+v", problems)
	}
}

func TestPreflight_Collision(t *testing.T) {
	home := t.TempDir()
	nsDir := t.TempDir()
	dest := filepath.Join(home, ".config", "nvim")

	s := state.State{Entries: map[state.Key]state.Entry{
		{Repo: "dotfiles", Namespace: "other"}: {Enabled: true, LinkedDests: []string{dest}},
	}}

	entries := []manifest.Entry{{Name: "nvim", Dest: dest}}
	key := state.Key{Repo: "dotfiles", Namespace: "editors"}
	problems, err := Preflight(key, nsDir, entries, s)
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if len(problems) != 1 || problems[0].Kind != Collision {
		t.Fatalf("expected one Collision problem, got %+v", problems)
	}
	if problems[0].Conflicting == nil || problems[0].Conflicting.Namespace != "other" {
		t.Fatalf("expected the conflicting namespace to be named, got %+v", problems[0].Conflicting)
	}
}

func TestPreflight_Collision_SameNamespaceNotAConflict(t *testing.T) {
	home := t.TempDir()
	nsDir := t.TempDir()
	dest := filepath.Join(home, ".config", "nvim")

	key := state.Key{Repo: "dotfiles", Namespace: "editors"}
	s := state.State{Entries: map[state.Key]state.Entry{
		key: {Enabled: true, LinkedDests: []string{dest}},
	}}

	entries := []manifest.Entry{{Name: "nvim", Dest: dest}}
	problems, err := Preflight(key, nsDir, entries, s)
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("expected re-enabling the same namespace not to conflict with itself, got %+v", problems)
	}
}

func TestPreflight_UnwritableParent_NamedAlongsideOtherProblems(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits behave differently on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses permission checks")
	}

	home := t.TempDir()
	nsDir := t.TempDir()

	locked := filepath.Join(home, "locked")
	if err := os.MkdirAll(locked, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0755) })

	unwritableDest := filepath.Join(locked, "hosts")
	occupiedDest := filepath.Join(home, "config")
	if err := os.WriteFile(occupiedDest, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	entries := []manifest.Entry{
		{Name: "hosts", Dest: unwritableDest},
		{Name: "config", Dest: occupiedDest},
	}
	key := state.Key{Repo: "dotfiles", Namespace: "editors"}
	problems, err := Preflight(key, nsDir, entries, emptyState())
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if len(problems) != 2 {
		t.Fatalf("expected the unwritable destination reported alongside the occupied one, got %+v", problems)
	}
	var sawUnwritable, sawOccupied bool
	for _, p := range problems {
		if p.Kind == Unwritable && p.Entry.Dest == unwritableDest {
			sawUnwritable = true
		}
		if p.Kind == Occupied && p.Entry.Dest == occupiedDest {
			sawOccupied = true
		}
	}
	if !sawUnwritable || !sawOccupied {
		t.Fatalf("expected both an Unwritable and an Occupied problem, got %+v", problems)
	}
}
