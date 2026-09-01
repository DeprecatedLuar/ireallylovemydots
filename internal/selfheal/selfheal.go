// Package selfheal reconciles machine state and the filesystem against the
// repository manifest, per concept.md "Self-healing": it never prompts and
// never destroys, and it never writes a manifest. It is single-purpose —
// correction only. Deciding what a correction means for a listing's markers
// is internal/commands' job, not this package's.
package selfheal

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/DeprecatedLuar/dotz/internal/link"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/paths"
	"github.com/DeprecatedLuar/dotz/internal/state"
)

// Problem is one destination self-heal found wrong but would not correct
// without risking data: a real file or directory occupying a spot a symlink
// belongs, or a destination it could not write to. Reported, never fixed —
// concept.md "Self-healing": "When it encounters a real file where a symlink
// belongs, it reports the conflict and moves on."
type Problem struct {
	Repo      string
	Namespace string
	Entry     string
	Dest      string
	Detail    string
}

// Run walks every namespace state records as enabled, verifying each linked
// destination with a single lstat and correcting drift in the order
// concept.md "Self-healing" describes:
//
//  1. Repository manifest corrects machine state: a destination state
//     recorded that no longer appears in the current manifest moved or was
//     dropped upstream — its stale symlink is removed so nothing dangling is
//     left behind.
//  2. Machine state corrects the filesystem: a destination the manifest
//     still names gets its symlink created if missing, or repointed if it
//     points somewhere else.
//
// A destination occupied by a real file or directory is reported as a
// Problem and left untouched. An entry with an empty destination is never
// linked and never reported here — concept.md "Manual edits": that is a
// listing's job, not self-heal's, since it is a manifest problem, not
// drift.
//
// Only namespaces recorded Enabled are reconciled. An installed-but-disabled
// namespace has no links to verify, and an uninstalled namespace ("=") is
// not drift by design — concept.md "An uninstalled namespace is not drift."
func Run() ([]Problem, error) {
	reg, err := manifest.ReadRegistry()
	if err != nil {
		return nil, err
	}
	s, err := state.Read()
	if err != nil {
		return nil, err
	}
	dataDir, err := paths.Data()
	if err != nil {
		return nil, err
	}

	repoDirs := make(map[string]string, len(reg.Repos))
	for _, r := range reg.Repos {
		repoDirs[r.Name] = filepath.Join(dataDir, r.Name)
	}

	var problems []Problem
	changed := false
	for key, entry := range s.Entries {
		if !entry.Enabled {
			continue
		}
		repoDir, ok := repoDirs[key.Repo]
		if !ok {
			// The repository is no longer registered. Nothing to reconcile
			// against; not this pass's job to guess at removal.
			continue
		}
		namespaceDir := filepath.Join(repoDir, key.Namespace)
		if _, statErr := os.Stat(namespaceDir); statErr != nil {
			// Not materialized on this machine despite state saying
			// enabled — nothing on disk to verify a link against.
			continue
		}

		m, readErr := manifest.Read(namespaceDir)
		if readErr != nil {
			return nil, readErr
		}

		linkedDests, nsProblems, reconcileErr := reconcileNamespace(key, namespaceDir, m.Entries, entry.LinkedDests)
		if reconcileErr != nil {
			return nil, reconcileErr
		}
		problems = append(problems, nsProblems...)

		if !sameDests(linkedDests, entry.LinkedDests) {
			entry.LinkedDests = linkedDests
			s.Entries[key] = entry
			changed = true
		}
	}

	if changed {
		if err := state.Write(s); err != nil {
			return nil, err
		}
	}

	sort.Slice(problems, func(i, j int) bool {
		if problems[i].Namespace != problems[j].Namespace {
			return problems[i].Namespace < problems[j].Namespace
		}
		return problems[i].Entry < problems[j].Entry
	})
	return problems, nil
}

// reconcileNamespace reconciles one enabled namespace's entries against its
// recorded links, returning the destinations verified linked afterward
// (state's new LinkedDests) and any conflicts it would not resolve.
func reconcileNamespace(key state.Key, namespaceDir string, entries []manifest.Entry, recorded []string) ([]string, []Problem, error) {
	current := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.Dest != "" {
			current[e.Dest] = true
		}
	}

	// Repository manifest corrects machine state: a destination this
	// namespace used to own but no longer names moved or was dropped
	// upstream. Its link is stale and gets removed, leaving no dangling
	// symlink behind.
	var problems []Problem
	for _, dest := range recorded {
		if current[dest] {
			continue
		}
		if err := removeStaleLink(dest); err != nil {
			problems = append(problems, Problem{Repo: key.Repo, Namespace: key.Namespace, Dest: dest, Detail: err.Error()})
		}
	}

	var linked []string
	for _, e := range entries {
		if e.Dest == "" {
			continue
		}
		target := filepath.Join(namespaceDir, e.Name)

		st, err := link.Classify(e.Dest, target)
		if err != nil {
			problems = append(problems, Problem{Repo: key.Repo, Namespace: key.Namespace, Entry: e.Name, Dest: e.Dest, Detail: err.Error()})
			continue
		}

		switch st {
		case link.CorrectSymlink:
			linked = append(linked, e.Dest)
		case link.Missing:
			// State corrects the filesystem: removal was never requested,
			// so a missing link is recreated.
			if err := createLink(e.Dest, target); err != nil {
				problems = append(problems, Problem{Repo: key.Repo, Namespace: key.Namespace, Entry: e.Name, Dest: e.Dest, Detail: err.Error()})
				continue
			}
			linked = append(linked, e.Dest)
		case link.WrongSymlink:
			// State corrects the filesystem: a link pointing somewhere
			// wrong is repointed.
			if err := link.Remove(e.Dest); err != nil {
				problems = append(problems, Problem{Repo: key.Repo, Namespace: key.Namespace, Entry: e.Name, Dest: e.Dest, Detail: err.Error()})
				continue
			}
			if err := createLink(e.Dest, target); err != nil {
				problems = append(problems, Problem{Repo: key.Repo, Namespace: key.Namespace, Entry: e.Name, Dest: e.Dest, Detail: err.Error()})
				continue
			}
			linked = append(linked, e.Dest)
		case link.RealFile, link.RealDir:
			// Never destroyed — reported and left alone, per concept.md
			// "Self-healing".
			problems = append(problems, Problem{Repo: key.Repo, Namespace: key.Namespace, Entry: e.Name, Dest: e.Dest, Detail: "real file or directory occupies the destination"})
		}
	}
	return linked, problems, nil
}

// removeStaleLink removes dest only when it is still a symlink. A user may
// have replaced the recorded destination with real data by hand; self-heal
// never destroys, so anything but a symlink there is left untouched.
func removeStaleLink(dest string) error {
	info, err := os.Lstat(dest)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lstat %s: %w", dest, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return nil
	}
	return link.Remove(dest)
}

func createLink(dest, target string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return fmt.Errorf("create parent directory for %s: %w", dest, err)
	}
	return link.Create(dest, target)
}

func sameDests(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]bool, len(a))
	for _, d := range a {
		set[d] = true
	}
	for _, d := range b {
		if !set[d] {
			return false
		}
	}
	return true
}
