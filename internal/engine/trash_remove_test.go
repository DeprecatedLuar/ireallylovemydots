package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DeprecatedLuar/ireallylovemydots/internal/manifest"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/state"
)

func TestTrashEntries_KeepsNameDestAndIgnore(t *testing.T) {
	nsDir := t.TempDir()
	for _, n := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(nsDir, n), []byte(n), 0644); err != nil {
			t.Fatal(err)
		}
	}
	m := manifest.Manifest{Name: "root", Dest: "/d", Ignore: true, Entries: []manifest.Entry{
		{Name: "a", Dest: manifest.DestNone}, {Name: "b", Dest: manifest.DestNone}}}
	if err := manifest.Write(nsDir, m); err != nil {
		t.Fatal(err)
	}
	key := state.Key{Repo: "r", Namespace: "root"}
	if _, err := TrashEntries(key, nsDir, []manifest.Entry{{Name: "a", Dest: manifest.DestNone}}, state.State{Entries: map[state.Key]state.Entry{}}); err != nil {
		t.Fatal(err)
	}
	got, err := manifest.Read(nsDir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "root" || got.Dest != "/d" || !got.Ignore || len(got.Entries) != 1 {
		t.Fatalf("manifest after TrashEntries = %+v", got)
	}
}
