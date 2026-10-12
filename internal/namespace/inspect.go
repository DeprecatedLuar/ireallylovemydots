package namespace

import (
	"os"
	"path/filepath"

	"github.com/DeprecatedLuar/ireallylovemydots/internal/manifest"
)

// dotsManifestFile and gitignoreFile name the two per-namespace files Inspect
// never reports as untracked payload: the manifest itself, and a namespace's
// own .gitignore, which is repo plumbing rather than tracked content (the
// root .gitignore gets the same treatment in repo/bootstrap.go's neverPlanned set).
// Duplicated as a literal rather than imported — manifest's own filename
// constant is unexported, and internal/repo/catalogue.go keeps its own copy
// of ".dots" for the same reason: not worth a cross-package export for one
// string each.
// profilesDir is the third: a namespace's profile layer is dots' own
// plumbing, not tracked content — concept.md reserves the dot-prefixed names
// .dots and .profiles inside a namespace. Its own contents are reconciled by
// self-heal's profile pass, not by this walk.
const (
	dotsManifestFile = ".dots"
	gitignoreFile    = ".gitignore"
	profilesDir      = ".profiles"
)

// Report is what one namespace folder holds, checked against its manifest —
// facts only, no markers and no notion of enabled/disabled. A listing turns
// these facts into "!" rows; self-heal turns them into a repair finding.
type Report struct {
	// ManifestMissing is true when the namespace has no .dots file at all,
	// as opposed to one that parses to zero entries — Read collapses that
	// distinction (manifest.go's Read), so Inspect checks it separately via
	// manifest.Exists.
	ManifestMissing bool
	// Invalid names every entry whose destination is empty — concept.md
	// "Manual edits": invalid, not pending.
	Invalid []string
	// Orphans names every entry whose payload is gone from the namespace
	// folder.
	Orphans []string
	// Untracked names every file in the namespace folder with no matching
	// manifest entry.
	Untracked []string
}

// Inspect walks nsDir once and classifies it against the manifest m (already
// read by the caller). Under a default destination every item is linked, so
// Untracked is empty. One directory read serves both listing's per-entry
// markers and self-heal's repair warning, so neither has to walk the
// namespace folder on its own.
func Inspect(nsDir string, m manifest.Manifest) (Report, error) {
	exists, err := manifest.Exists(nsDir)
	if err != nil {
		return Report{}, err
	}

	tracked := make(map[string]bool, len(m.Entries))
	var invalid, orphans []string
	for _, e := range m.Entries {
		tracked[e.Name] = true
		if e.Dest == "" {
			invalid = append(invalid, e.Name)
			continue
		}
		payload := filepath.Join(nsDir, e.Name)
		if _, statErr := os.Lstat(payload); os.IsNotExist(statErr) {
			orphans = append(orphans, e.Name)
		}
	}

	var untracked []string
	if m.Dest == "" {
		items, err := Items(nsDir, m.Name != "")
		if err != nil {
			return Report{}, err
		}
		for _, name := range items {
			if !tracked[name] {
				untracked = append(untracked, name)
			}
		}
	}

	return Report{
		ManifestMissing: !exists,
		Invalid:         invalid,
		Orphans:         orphans,
		Untracked:       untracked,
	}, nil
}
