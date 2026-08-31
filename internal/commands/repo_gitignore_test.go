package commands

import (
	"strings"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/repo"
)

// TestRenderGitignorePreview_ShowsAllThreeOutcomes covers concept.md
// "Bootstrap rewrites the root .gitignore": all three outcomes — rewritten,
// unchanged, and unmapped — must appear in the preview before the
// confirmation prompt, since that is the only place the conversion is
// agreed to.
func TestRenderGitignorePreview_ShowsAllThreeOutcomes(t *testing.T) {
	changes := []repo.GitignoreChange{
		{Outcome: repo.GitignoreRewritten, Original: "copyq/copyq.lock", Rewritten: "copyq/copyq/copyq.lock"},
		{Outcome: repo.GitignoreUnchanged, Original: "*.lock", Rewritten: "*.lock"},
		{Outcome: repo.GitignoreUnmapped, Original: "config/copyq/copyq.lock", Rewritten: "config/copyq/copyq.lock"},
	}
	got := renderGitignorePreview(changes)
	for _, want := range []string{
		".gitignore",
		"rewritten   copyq/copyq.lock -> copyq/copyq/copyq.lock",
		"unchanged   *.lock",
		"unmapped    config/copyq/copyq.lock",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected the preview to contain %q, got:\n%s", want, got)
		}
	}
}

func TestRenderGitignorePreview_NoChangesIsEmpty(t *testing.T) {
	if got := renderGitignorePreview(nil); got != "" {
		t.Fatalf("expected an empty preview for no changes, got %q", got)
	}
}

func TestRenderGitignorePreview_OnlyBlankAndCommentLinesIsEmpty(t *testing.T) {
	changes := []repo.GitignoreChange{
		{Outcome: repo.GitignoreUnchanged, Original: "", Rewritten: ""},
		{Outcome: repo.GitignoreUnchanged, Original: "# a comment", Rewritten: "# a comment"},
	}
	if got := renderGitignorePreview(changes); got != "" {
		t.Fatalf("expected blank/comment lines not worth previewing, got %q", got)
	}
}
