package namespace

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/DeprecatedLuar/dotz/internal/link"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
)

// RebuildFromLinks reconstructs manifest entries from destinations that are
// still symlinks pointing inside nsDir — proof, not inference: readlink
// hands back both halves of the entry (the name from the target's basename,
// the destination from the recorded path itself). A destination that is
// missing, not a symlink, or points somewhere other than nsDir contributes
// nothing; it is left for Scaffold, or for the caller to treat as
// unrecoverable.
func RebuildFromLinks(nsDir string, linkedDests []string) []manifest.Entry {
	prefix := nsDir + string(os.PathSeparator)

	var entries []manifest.Entry
	for _, dest := range linkedDests {
		target, isSymlink, err := link.ReadIfSymlink(dest)
		if err != nil || !isSymlink || !strings.HasPrefix(target, prefix) {
			continue
		}
		entries = append(entries, manifest.Entry{Name: filepath.Base(target), Dest: dest})
	}
	return entries
}

// Scaffold returns one dest-less entry per untracked payload in report — so
// a recovered manifest names every payload in the namespace folder, with the
// ones RebuildFromLinks could not prove a destination for landing as invalid
// entries (concept.md "Manual edits": "!", not linked, until a human fills
// in a destination or removes the payload). Call Inspect with whatever
// RebuildFromLinks already recovered before building report, so its
// Untracked list already excludes them.
func Scaffold(report Report) []manifest.Entry {
	entries := make([]manifest.Entry, 0, len(report.Untracked))
	for _, name := range report.Untracked {
		entries = append(entries, manifest.Entry{Name: name, Dest: ""})
	}
	return entries
}
