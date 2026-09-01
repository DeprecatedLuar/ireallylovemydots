package commands

import (
	"fmt"
	"os"

	"github.com/DeprecatedLuar/dotz/internal/selfheal"
	"github.com/DeprecatedLuar/dotz/internal/ui"
)

// RenderSelfHealFindings prints self-heal's findings to stderr, per
// concept.md "Self-healing": self-heal itself never prints, so main.go
// hands its result here after every invocation. Problem lines are left to
// listing, which already surfaces drift per entry via the "!" marker;
// RenderSelfHealFindings covers only what a listing cannot see on its own —
// an unregistered clone, a dropped state entry, or a data directory that
// looks empty — since those are evidence about the data directory itself,
// not about any one namespace.
func RenderSelfHealFindings(f selfheal.Findings) {
	if f.DataDirEmpty {
		fmt.Fprintln(os.Stderr, ui.ErrorTone("! the data directory holds no repository clones — check XDG_DATA_HOME before continuing"))
		return
	}
	for _, name := range f.Unregistered {
		fmt.Fprintln(os.Stderr, ui.ErrorTone(fmt.Sprintf("! %s  unregistered clone in the data directory — run: dots repo adopt %s", name, name)))
	}
	for _, key := range f.Dropped {
		fmt.Fprintln(os.Stderr, ui.Tip(fmt.Sprintf("dropped stale state for %s/%s — its repository no longer exists", key.Repo, key.Namespace)))
	}
}
