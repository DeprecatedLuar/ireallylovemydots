package state

import (
	"encoding/json"
	"os"
	"testing"
)

func TestWrite_StaysJSON(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	s := State{Entries: map[Key]Entry{
		{Repo: "dotfiles", Namespace: "nvim"}: {Enabled: true},
	}}
	if err := Write(s); err != nil {
		t.Fatalf("Write error: %v", err)
	}

	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []record
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("on-disk state is not valid JSON: %v", err)
	}
}

func TestRoundTrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	s, err := Read()
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	key := Key{Repo: "dotfiles", Namespace: "nvim"}
	s.Entries[key] = Entry{Enabled: true, ActiveProfile: "work", LinkedDests: []string{"/home/u/.config/nvim"}}

	if err := Write(s); err != nil {
		t.Fatalf("Write error: %v", err)
	}

	got, err := Read()
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	entry, ok := got.Entries[key]
	if !ok {
		t.Fatalf("expected entry for %+v", key)
	}
	if !entry.Enabled || entry.ActiveProfile != "work" || len(entry.LinkedDests) != 1 {
		t.Fatalf("got %+v, mismatch", entry)
	}
}

func TestRead_MissingFileIsEmpty(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	s, err := Read()
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if len(s.Entries) != 0 {
		t.Fatalf("got %d entries, want 0", len(s.Entries))
	}
}
