package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/manifest"
)

// TestRelink_RepointsDanglingLink covers the exact drift a repository or
// namespace rename produces: the payload a destination's symlink points at
// has moved (simulated here by writing a symlink that targets a path under
// a now-stale directory), and Relink must repoint it at the entry's current
// payload without being asked to roll anything back.
func TestRelink_RepointsDanglingLink(t *testing.T) {
	nsDir := t.TempDir()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(nsDir, "cfg"), []byte("payload"), 0644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(home, "cfg")
	stale := filepath.Join(t.TempDir(), "cfg") // a payload path that no longer exists
	if err := os.Symlink(stale, dest); err != nil {
		t.Fatal(err)
	}

	entries := []manifest.Entry{{Name: "cfg", Dest: dest}}
	linked, failures, err := Relink(nsDir, entries, "")
	if err != nil {
		t.Fatalf("Relink: %v", err)
	}
	if len(failures) != 0 {
		t.Fatalf("failures = %+v, want none", failures)
	}
	if len(linked) != 1 || linked[0] != dest {
		t.Fatalf("linked = %v, want [%s]", linked, dest)
	}

	target, err := os.Readlink(dest)
	if err != nil {
		t.Fatal(err)
	}
	if target != filepath.Join(nsDir, "cfg") {
		t.Fatalf("target = %q, want the current payload path", target)
	}
}

// TestRelink_RealFileReportedNotDestroyed covers self-heal's amplifier: if a
// destination has been replaced by a real file (e.g. an app recreating its
// config after finding a dangling link), Relink must report it and leave it
// untouched rather than trashing it — the same "never destroyed" rule
// self-heal itself follows.
func TestRelink_RealFileReportedNotDestroyed(t *testing.T) {
	nsDir := t.TempDir()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(nsDir, "cfg"), []byte("payload"), 0644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(home, "cfg")
	if err := os.WriteFile(dest, []byte("real user data"), 0644); err != nil {
		t.Fatal(err)
	}

	entries := []manifest.Entry{{Name: "cfg", Dest: dest}}
	linked, failures, err := Relink(nsDir, entries, "")
	if err != nil {
		t.Fatalf("Relink: %v", err)
	}
	if len(linked) != 0 {
		t.Fatalf("linked = %v, want none", linked)
	}
	if len(failures) != 1 || failures[0].Dest != dest {
		t.Fatalf("failures = %+v, want one naming %s", failures, dest)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "real user data" {
		t.Fatal("real file at the destination was modified")
	}
}

// TestRelink_PartialFailureDoesNotRollBackWhatSucceeded covers Relink's
// best-effort policy: a namespace with one repairable entry and one blocked
// entry must still end up with the repairable one relinked — unlike
// SwitchProfile, nothing here is rolled back, since the whole point of
// calling Relink is that the links were already wrong going in.
func TestRelink_PartialFailureDoesNotRollBackWhatSucceeded(t *testing.T) {
	nsDir := t.TempDir()
	home := t.TempDir()
	for _, name := range []string{"good", "blocked"} {
		if err := os.WriteFile(filepath.Join(nsDir, name), []byte("payload "+name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	goodDest := filepath.Join(home, "good")
	stale := filepath.Join(t.TempDir(), "good")
	if err := os.Symlink(stale, goodDest); err != nil {
		t.Fatal(err)
	}
	blockedDest := filepath.Join(home, "blocked")
	if err := os.WriteFile(blockedDest, []byte("occupied"), 0644); err != nil {
		t.Fatal(err)
	}

	entries := []manifest.Entry{
		{Name: "good", Dest: goodDest},
		{Name: "blocked", Dest: blockedDest},
	}
	linked, failures, err := Relink(nsDir, entries, "")
	if err != nil {
		t.Fatalf("Relink: %v", err)
	}
	if len(linked) != 1 || linked[0] != goodDest {
		t.Fatalf("linked = %v, want only %s repaired despite blocked's failure", linked, goodDest)
	}
	if len(failures) != 1 || failures[0].Dest != blockedDest {
		t.Fatalf("failures = %+v, want one naming %s", failures, blockedDest)
	}

	target, err := os.Readlink(goodDest)
	if err != nil {
		t.Fatal(err)
	}
	if target != filepath.Join(nsDir, "good") {
		t.Fatal("good's repointed link was rolled back despite blocked's failure")
	}
}

// TestRelink_DuplicateDestinationDoesNotStompFirstEntry covers the copyq
// bug: two entries naming the same destination must not have the second
// silently overwrite the link the first just created — that read as "both
// linked" to self-heal's own-entry count (len(linked) == len(entries)) when
// only one of the two destinations actually holds either entry's payload.
// The first entry (manifest order) keeps the destination; the second is
// reported as a failure rather than acted on.
func TestRelink_DuplicateDestinationDoesNotStompFirstEntry(t *testing.T) {
	nsDir := t.TempDir()
	home := t.TempDir()
	for _, name := range []string{"first", "second"} {
		if err := os.WriteFile(filepath.Join(nsDir, name), []byte("payload "+name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	dest := filepath.Join(home, "shared")

	entries := []manifest.Entry{
		{Name: "first", Dest: dest},
		{Name: "second", Dest: dest},
	}
	linked, failures, err := Relink(nsDir, entries, "")
	if err != nil {
		t.Fatalf("Relink: %v", err)
	}
	if len(linked) != 1 || linked[0] != dest {
		t.Fatalf("linked = %v, want exactly one claim on %s", linked, dest)
	}
	if len(failures) != 1 || failures[0].Entry.Name != "second" {
		t.Fatalf("failures = %+v, want one naming the second entry", failures)
	}

	target, err := os.Readlink(dest)
	if err != nil {
		t.Fatal(err)
	}
	if target != filepath.Join(nsDir, "first") {
		t.Fatalf("target = %q, want the first entry's payload left in place, not overwritten", target)
	}
}
