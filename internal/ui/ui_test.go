package ui

import "testing"

// go test's stdout/stderr are never a character device, so these exercise
// exactly the piped-output path concept.md requires: plain text, markers
// included, no escape sequences — regardless of NO_COLOR. The terminal path
// (colour emitted, then suppressed again by NO_COLOR) is smoke-tested
// manually against a real TTY, since faking one requires a pty this
// package's tests do not set up.
func TestRender_PipedOutputHasNoEscapeSequences(t *testing.T) {
	entries := []Entry{
		{Marker: MarkerEnabled, Name: "nvim"},
		{Marker: MarkerMaterialized, Name: "kitty"},
		{Marker: MarkerAbsent, Name: "tmux"},
		{Marker: MarkerProblem, Name: "zsh"},
		{Marker: MarkerUntracked, Name: "starship.toml"},
	}
	got := Render(entries)
	want := "+ nvim\n- kitty\n= tmux\n! zsh\n? starship.toml\n"
	if got != want {
		t.Fatalf("Render (piped) = %q, want %q", got, want)
	}
}

func TestRender_PipedOutputUnaffectedByNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	got := Render([]Entry{{Marker: MarkerEnabled, Name: "nvim"}})
	if got != "+ nvim\n" {
		t.Fatalf("Render (piped, NO_COLOR) = %q, want %q", got, "+ nvim\n")
	}
}

func TestErrorTone_PlainWhenNotATerminal(t *testing.T) {
	got := ErrorTone("something is wrong")
	if got != "something is wrong" {
		t.Fatalf("ErrorTone (piped) = %q, want plain text unchanged", got)
	}
}

func TestWarningTone_PlainWhenNotATerminal(t *testing.T) {
	got := WarningTone("something is unclear")
	if got != "something is unclear" {
		t.Fatalf("WarningTone (piped) = %q, want plain text unchanged", got)
	}
}
