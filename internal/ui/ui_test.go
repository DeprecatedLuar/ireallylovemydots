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

func TestOperation_WithAndWithoutDetail(t *testing.T) {
	if got, want := Operation(MarkerEnabled, "nvim", ""), "+ nvim\n"; got != want {
		t.Fatalf("Operation (no detail) = %q, want %q", got, want)
	}
	got := Operation(MarkerProblem, "nvim", "~/.config/nvim    real directory, 340 files")
	want := "! nvim    ~/.config/nvim    real directory, 340 files\n"
	if got != want {
		t.Fatalf("Operation (detail) = %q, want %q", got, want)
	}
}

func TestSub_IsIndentedUnderItsOperationLine(t *testing.T) {
	op := Operation(MarkerEnabled, "nvim", "")
	sub := Sub(MarkerRemoved, "~/.config/nvim", "real directory, 340 files -> trash")
	got := op + sub
	want := "+ nvim\n  x ~/.config/nvim    real directory, 340 files -> trash\n"
	if got != want {
		t.Fatalf("Operation+Sub = %q, want %q", got, want)
	}
}

func TestReport_JoinsLinesAndFooter(t *testing.T) {
	lines := []string{Operation(MarkerEnabled, "nvim", ""), Operation(MarkerProblem, "zsh", "occupied")}
	got := Report(lines, "1 enabled, 1 skipped.")
	want := "+ nvim\n! zsh    occupied\n\n1 enabled, 1 skipped.\n"
	if got != want {
		t.Fatalf("Report = %q, want %q", got, want)
	}
}

func TestReport_NoFooterOmitsBlankLine(t *testing.T) {
	lines := []string{Operation(MarkerEnabled, "nvim", "")}
	got := Report(lines, "")
	if got != "+ nvim\n" {
		t.Fatalf("Report (no footer) = %q, want %q", got, "+ nvim\n")
	}
}

func TestConfirm_AssemblesPromptLinesAndTip(t *testing.T) {
	got := Confirm("Remove \"nvim\"?", []string{"1 file(s) -> trash"}, "Tip: --restore puts the files back first.")
	want := "Remove \"nvim\"?\n  1 file(s) -> trash\n\n  Tip: --restore puts the files back first."
	if got != want {
		t.Fatalf("Confirm = %q, want %q", got, want)
	}
}

func TestConfirm_NoTipOmitsTrailingBlock(t *testing.T) {
	got := Confirm("Remove \"nvim\"?", []string{"1 file(s) -> trash"}, "")
	want := "Remove \"nvim\"?\n  1 file(s) -> trash"
	if got != want {
		t.Fatalf("Confirm (no tip) = %q, want %q", got, want)
	}
}

func TestMarkerRemoved_UsesErrorTone(t *testing.T) {
	// Piped output stays plain regardless of the tone table, but Operation
	// must still recognize "x" as a known marker rather than silently
	// falling through — this exercises that path even though the escape
	// sequence itself is invisible here (go test's stdout is never a
	// terminal, per the package-level comment above).
	got := Operation(MarkerRemoved, "editors", "")
	if got != "x editors\n" {
		t.Fatalf("Operation(MarkerRemoved) = %q, want %q", got, "x editors\n")
	}
}
