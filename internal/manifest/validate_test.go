package manifest

import "testing"

// TestValidate_CleanManifestProducesNothing covers the ordinary case: no
// entry shares or nests inside another's destination, no name repeats.
func TestValidate_CleanManifestProducesNothing(t *testing.T) {
	m := Manifest{Entries: []Entry{
		{Name: "nvim", Dest: "/home/u/.config/nvim"},
		{Name: "kitty", Dest: "/home/u/.config/kitty"},
	}}
	if problems := Validate(m); len(problems) != 0 {
		t.Fatalf("expected no problems, got %+v", problems)
	}
}

// TestValidate_DuplicateDestinationFlagsBothEntries covers the copyq case:
// two entries naming the exact same destination — the in-repo link guard's
// degenerate, equal-path form.
func TestValidate_DuplicateDestinationFlagsBothEntries(t *testing.T) {
	m := Manifest{Entries: []Entry{
		{Name: "copyq-commands.ini", Dest: "/home/u/.config/copyq"},
		{Name: "copyq.conf", Dest: "/home/u/.config/copyq"},
	}}
	problems := Validate(m)
	if len(problems) != 2 {
		t.Fatalf("expected both entries flagged, got %+v", problems)
	}
	byEntry := map[string]bool{}
	for _, p := range problems {
		byEntry[p.Entry] = true
	}
	if !byEntry["copyq-commands.ini"] || !byEntry["copyq.conf"] {
		t.Fatalf("expected both entry names flagged, got %+v", problems)
	}
}

// TestValidate_ContainedDestinationFlagsBothEntries covers the ordinary
// in-repo link guard case: one entry's destination is a directory, another's
// falls beneath it — after the first links, the second would resolve
// through it back into the repository.
func TestValidate_ContainedDestinationFlagsBothEntries(t *testing.T) {
	m := Manifest{Entries: []Entry{
		{Name: "nvim", Dest: "/home/u/.config/nvim"},
		{Name: "init.lua", Dest: "/home/u/.config/nvim/init.lua"},
	}}
	problems := Validate(m)
	if len(problems) != 2 {
		t.Fatalf("expected both entries flagged, got %+v", problems)
	}
}

// TestValidate_DuplicateEntryName covers a manifest with two entries sharing
// a name, unreadable by any by-name lookup.
func TestValidate_DuplicateEntryName(t *testing.T) {
	m := Manifest{Entries: []Entry{
		{Name: "nvim", Dest: "/home/u/.config/nvim"},
		{Name: "nvim", Dest: "/home/u/.config/nvim2"},
	}}
	problems := Validate(m)
	if len(problems) != 2 {
		t.Fatalf("expected both duplicate-named entries flagged, got %+v", problems)
	}
}

// TestValidate_EmptyOrNoneDestinationsNeverFlagged covers concept.md "Manual
// edits": an empty destination is already invalid on its own, and DestNone
// is deliberately unlinked — neither can collide with anything, including
// each other.
func TestValidate_EmptyOrNoneDestinationsNeverFlagged(t *testing.T) {
	m := Manifest{Entries: []Entry{
		{Name: "a", Dest: ""},
		{Name: "b", Dest: ""},
		{Name: "c", Dest: DestNone},
		{Name: "d", Dest: DestNone},
	}}
	if problems := Validate(m); len(problems) != 0 {
		t.Fatalf("expected no problems, got %+v", problems)
	}
}
