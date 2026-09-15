package state

import "testing"

func TestReadAccess_MissingFileIsEmpty(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	a, err := ReadAccess()
	if err != nil {
		t.Fatalf("ReadAccess error: %v", err)
	}
	if len(a.ReadOnly) != 0 {
		t.Fatalf("expected empty access, got %+v", a.ReadOnly)
	}
}

func TestAccessRoundTrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	a, err := ReadAccess()
	if err != nil {
		t.Fatalf("ReadAccess error: %v", err)
	}
	a.SetReadOnly("dotfiles", true)

	if err := WriteAccess(a); err != nil {
		t.Fatalf("WriteAccess error: %v", err)
	}

	got, err := ReadAccess()
	if err != nil {
		t.Fatalf("ReadAccess error: %v", err)
	}
	if !got.IsReadOnly("dotfiles") {
		t.Fatalf("expected dotfiles to be read-only after round-trip")
	}
}

func TestIsReadOnly_AbsentNameIsPushable(t *testing.T) {
	a := Access{ReadOnly: map[string]bool{"other": true}}
	if a.IsReadOnly("dotfiles") {
		t.Fatalf("expected a name absent from the map to be pushable")
	}
}
