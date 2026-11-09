package ui

import "testing"

func TestTree_CollapsesChainsAndMarksLeaves(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	got := Tree([]TreeNode{
		{Name: "luxos", LeafMarker: MarkerProblem, Paths: []string{
			".local/hosts/cathode/modules/default.nix",
			".local/hosts/cathode/flake.lock",
		}},
		{Name: "noctalia", LeafMarker: MarkerProblem, Paths: []string{"settings.toml"}},
	})
	want := "" +
		"  ├── luxos\n" +
		"  │   └── .local/hosts/cathode\n" +
		"  │       ├── ! flake.lock\n" +
		"  │       └── ! modules/default.nix\n" +
		"  └── noctalia\n" +
		"      └── ! settings.toml\n"
	if got != want {
		t.Fatalf("Tree =\n%s\nwant\n%s", got, want)
	}
}

func TestTree_MarkedAndDimNodesWithoutChildren(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	got := Tree([]TreeNode{
		{Name: "README", Marker: MarkerProblem},
		{Name: "kitty", Marker: MarkerRemovedUpstream},
		{Name: "nvim", Dim: true},
	})
	want := "  ├── ! README\n  ├── ⊘ kitty\n  └── nvim\n"
	if got != want {
		t.Fatalf("Tree =\n%q\nwant\n%q", got, want)
	}
}

func TestTree_Empty(t *testing.T) {
	if got := Tree(nil); got != "" {
		t.Fatalf("Tree(nil) = %q, want empty", got)
	}
}

func TestSyncLine(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if got := SyncLine(true, "dots", ""); got != "✓ dots\n" {
		t.Fatalf("ok line = %q", got)
	}
	if got := SyncLine(false, "private-dots", "fetch failed"); got != "✗ private-dots (fetch failed)\n" {
		t.Fatalf("failed line = %q", got)
	}
	if got := SyncLine(false, "repo", ""); got != "✗ repo\n" {
		t.Fatalf("bare failed line = %q", got)
	}
}

func TestNote_IndentsEveryLine(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if got := Note("first\nsecond"); got != "  first\n  second\n" {
		t.Fatalf("Note = %q", got)
	}
}
