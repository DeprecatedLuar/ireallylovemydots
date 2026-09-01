// Package selfheal reconciles machine state and the filesystem against the
// repository manifest, per concept.md "Self-healing": it detects and
// reports, and it never prompts or destroys data. It is single-purpose —
// correction only. Deciding what a finding means for a listing's markers,
// or turning a report into an action, is internal/commands' job, not this
// package's.
package selfheal

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

// Findings is everything one Run pass discovered: drift it corrected,
// drift it left for the command layer to report, and any evidence one
// direction of the hierarchy has drifted from another. Only Problems and
// Unregistered are ever shown to the user unprompted, since only they name
// something the user can still act on — Dropped is history by the time Run
// returns.
type Findings struct {
	// Problems is unchanged from before: a real file or directory occupying
	// a destination a symlink belongs at, left untouched.
	Problems []Problem
	// Unregistered names every directory in the data directory that holds
	// no matching entry in the repository manifest — concept.md
	// "Self-healing": reported on every invocation, never auto-registered.
	// The fix is `repo adopt <name>`.
	Unregistered []string
	// Dropped lists every state entry removed this pass because its
	// repository's directory no longer exists in the data directory.
	Dropped []state.Key
	// DataDirEmpty is true when the data directory holds no repository
	// clones at all, which reads identically to every repository having
	// vanished at once — the wrong-XDG_DATA_HOME / unmounted-disk case.
	// When true, Run reconciles nothing: no drops, no unregistered scan.
	DataDirEmpty bool
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
//
// Two more directions are checked against the data directory itself, guarded
// by DataDirEmpty so a wrong XDG_DATA_HOME can never be mistaken for every
// repository having been removed — concept.md "The data directory can drift
// from the registry too":
//
//   - a directory there with no registry entry is reported (Unregistered),
//     never auto-registered;
//   - a state entry whose repository directory is gone is dropped, after
//     removing only the linked destinations self-heal can prove were its
//     own — a symlink still pointing inside that now-missing directory.
func Run() (Findings, error) {
	reg, err := manifest.ReadRegistry()
	if err != nil {
		return Findings{}, err
	}
	s, err := state.Read()
	if err != nil {
		return Findings{}, err
	}
	dataDir, err := paths.Data()
	if err != nil {
		return Findings{}, err
	}

	dataDirEntries, err := os.ReadDir(dataDir)
	if err != nil {
		return Findings{}, fmt.Errorf("read data directory %s: %w", dataDir, err)
	}
	present := make(map[string]bool, len(dataDirEntries))
	for _, e := range dataDirEntries {
		if e.IsDir() {
			present[e.Name()] = true
		}
	}
	if len(present) == 0 {
		return Findings{DataDirEmpty: true}, nil
	}

	registered := make(map[string]string, len(reg.Repos))
	for _, r := range reg.Repos {
		registered[r.Name] = filepath.Join(dataDir, r.Name)
	}

	var unregistered []string
	for name := range present {
		if _, ok := registered[name]; !ok {
			unregistered = append(unregistered, name)
		}
	}
	sort.Strings(unregistered)

	var problems []Problem
	var dropped []state.Key
	changed := false
	for key, entry := range s.Entries {
		if !entry.Enabled {
			continue
		}

		if !present[key.Repo] {
			// The repository's directory is gone from the data directory —
			// unambiguous once the DataDirEmpty guard above has passed,
			// since other repositories are still present. Its enabled
			// intent is meaningless without a repository to enable.
			stranded := cleanStrandedLinks(dataDir, key, entry.LinkedDests)
			problems = append(problems, stranded...)
			delete(s.Entries, key)
			dropped = append(dropped, key)
			changed = true
			continue
		}

		repoDir, ok := registered[key.Repo]
		if !ok {
			// The directory exists but is not registered — an
			// Unregistered finding above already names it. Left alone
			// rather than reconciled: whether these links are still
			// correct is not this pass's call to make on behalf of a
			// repository the user has not confirmed.
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
			return Findings{}, readErr
		}

		linkedDests, nsProblems, reconcileErr := reconcileNamespace(key, namespaceDir, m.Entries, entry.LinkedDests)
		if reconcileErr != nil {
			return Findings{}, reconcileErr
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
			return Findings{}, err
		}
	}

	sort.Slice(problems, func(i, j int) bool {
		if problems[i].Namespace != problems[j].Namespace {
			return problems[i].Namespace < problems[j].Namespace
		}
		return problems[i].Entry < problems[j].Entry
	})
	sort.Slice(dropped, func(i, j int) bool {
		if dropped[i].Repo != dropped[j].Repo {
			return dropped[i].Repo < dropped[j].Repo
		}
		return dropped[i].Namespace < dropped[j].Namespace
	})
	return Findings{Problems: problems, Unregistered: unregistered, Dropped: dropped}, nil
}

// cleanStrandedLinks removes, from a state entry whose repository directory
// no longer exists, every recorded destination self-heal can prove was its
// own: still a symlink, still pointing inside that now-missing directory.
// A target inside a directory that does not exist is necessarily dangling —
// nothing else could be at the other end. A destination that has since
// become a real file or directory, or a symlink repointed elsewhere, is left
// exactly as found; it stopped being dots' the moment it changed. Removal
// failures are reported as Problems but never block dropping the state
// entry itself, since the entry is meaningless either way once its
// repository is gone.
func cleanStrandedLinks(dataDir string, key state.Key, dests []string) []Problem {
	prefix := filepath.Join(dataDir, key.Repo) + string(os.PathSeparator)
	var problems []Problem
	for _, dest := range dests {
		info, err := os.Lstat(dest)
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		target, err := os.Readlink(dest)
		if err != nil {
			problems = append(problems, Problem{Repo: key.Repo, Namespace: key.Namespace, Dest: dest, Detail: err.Error()})
			continue
		}
		if !strings.HasPrefix(target, prefix) {
			continue
		}
		if err := link.Remove(dest); err != nil {
			problems = append(problems, Problem{Repo: key.Repo, Namespace: key.Namespace, Dest: dest, Detail: err.Error()})
		}
	}
	return problems
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
