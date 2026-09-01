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

	"github.com/DeprecatedLuar/dotz/internal/engine"
	"github.com/DeprecatedLuar/dotz/internal/git"
	"github.com/DeprecatedLuar/dotz/internal/link"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/namespace"
	"github.com/DeprecatedLuar/dotz/internal/paths"
	"github.com/DeprecatedLuar/dotz/internal/state"
)

// recoveryRev is the git revision manifest recovery reads a lost .dots from.
// Always HEAD: self-heal recovers what was last committed, never an
// uncommitted edit — there is nothing else it could safely mean.
const recoveryRev = "HEAD"

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

// RecoverySource names the evidence a rebuilt manifest came from, ordered by
// fidelity: git's committed copy is exact, live symlinks are proof for
// whatever they cover, and a scaffold is a bare naming of what is left.
type RecoverySource int

const (
	RecoveredFromGit RecoverySource = iota
	RecoveredFromLinks
	RecoveredScaffoldOnly
)

// Recovery records that Run rebuilt one namespace's missing manifest this
// pass, and how — concept.md "Manual edits": an absent manifest is unknown,
// not empty, so it is reconstructed from the best evidence on hand rather
// than read as "every entry was cleared."
type Recovery struct {
	Repo      string
	Namespace string
	Source    RecoverySource
}

// Repair is one namespace whose manifest — freshly recovered or not — still
// needs a human decision: entries with no destination, an orphaned entry, or
// an untracked payload. namespace.Inspect's Report carries the detail; the
// command layer decides how to phrase it, since self-heal itself never
// prints.
type Repair struct {
	Repo      string
	Namespace string
	Report    namespace.Report
}

// needsRepair reports whether report names anything a human still has to
// resolve by hand.
func needsRepair(report namespace.Report) bool {
	return report.ManifestMissing || len(report.Invalid) > 0 || len(report.Orphans) > 0 || len(report.Untracked) > 0
}

// Findings is everything one Run pass discovered: drift it corrected,
// drift it left for the command layer to report, and any evidence one
// direction of the hierarchy has drifted from another. Only Problems,
// Unregistered, Recovered, NeedsRepair, and Disabled are ever shown to the
// user unprompted, since only they name something the user can still act
// on — Dropped is history by the time Run returns.
type Findings struct {
	// Problems names every entry-scoped failure that is not itself an
	// auto-disable: a stranded-link removal failure or a stale-link removal
	// failure. A namespace that could not be kept fully linked no longer
	// appears here — see Disabled.
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
	// Recovered lists every namespace whose missing manifest was rebuilt
	// this pass, and from what evidence.
	Recovered []Recovery
	// NeedsRepair lists every enabled namespace whose manifest — recovered
	// this pass or not — still holds something only a human can resolve.
	NeedsRepair []Repair
	// Disabled lists every namespace Run flipped to disabled this pass
	// because it could not bring every entry's symlink into place —
	// concept.md "Self-healing": "enabled means every entry's symlink is
	// correct and healthy — nothing less."
	Disabled []Disabled
}

// Disabled records one namespace Run flipped to disabled this pass, by
// calling the same engine.Disable an explicit `dots disable` uses, because
// at least one entry's destination could not be linked. Reasons names the
// entry-scoped problems that blocked it, for the command layer to report
// alongside the disable.
type Disabled struct {
	Repo      string
	Namespace string
	Reasons   []Problem
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
// A destination occupied by a real file or directory, or otherwise
// unwritable, is never destroyed. Instead, once reconciliation finishes,
// Run checks whether every entry with a non-empty destination actually
// ended up linked; if not, the namespace is disabled — concept.md
// "Self-healing": "enabled means every entry's symlink is correct and
// healthy — nothing less" — and the entry-scoped problems that blocked it
// are reported as a Disabled finding rather than a bare Problem. An entry
// with an empty destination is never linked and never counted toward that
// check — concept.md "Manual edits": that is a listing's job, not
// self-heal's, since it is a manifest problem, not drift.
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
	var recovered []Recovery
	var repairs []Repair
	var disabled []Disabled
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

		exists, existsErr := manifest.Exists(namespaceDir)
		if existsErr != nil {
			return Findings{}, existsErr
		}

		var m manifest.Manifest
		if exists {
			read, readErr := manifest.Read(namespaceDir)
			if readErr != nil {
				return Findings{}, readErr
			}
			m = read
		} else {
			rebuilt, recovery, recoverErr := recoverManifest(repoDir, namespaceDir, key, entry.LinkedDests)
			if recoverErr != nil {
				return Findings{}, recoverErr
			}
			if writeErr := manifest.Write(namespaceDir, rebuilt); writeErr != nil {
				return Findings{}, writeErr
			}
			m = rebuilt
			recovered = append(recovered, recovery)
		}

		// A namespace that declared itself out of scope is skipped entirely
		// — no inspection, no repair finding, no reconciliation — per
		// concept.md "Namespace". State recording it Enabled regardless (only
		// reachable by hand-editing state, since `ignore` refuses an enabled
		// namespace and enable refuses an ignored one) is a contradiction
		// self-heal reports rather than silently acting on either side of.
		if m.Ignore {
			if entry.Enabled {
				problems = append(problems, Problem{Repo: key.Repo, Namespace: key.Namespace, Detail: "namespace is ignored (ignore = true) but machine state records it enabled"})
			}
			continue
		}

		report, inspectErr := namespace.Inspect(namespaceDir, m.Entries)
		if inspectErr != nil {
			return Findings{}, inspectErr
		}
		if needsRepair(report) {
			repairs = append(repairs, Repair{Repo: key.Repo, Namespace: key.Namespace, Report: report})
		}

		// A manifest recovered this pass is not yet trustworthy about what
		// it no longer claims — see recoverManifest's doc comment — so the
		// stale-link removal half of reconciliation is skipped for it this
		// one run. Everything else (creating or repointing a link for an
		// entry the recovered manifest does name) still runs normally.
		linkedDests, nsProblems, reconcileErr := reconcileNamespace(key, namespaceDir, m.Entries, entry.LinkedDests, !exists)
		if reconcileErr != nil {
			return Findings{}, reconcileErr
		}

		// Enabled means every entry's symlink is correct and healthy —
		// nothing less (concept.md "Self-healing"). want counts entries
		// this namespace could possibly link (a manifest problem's
		// empty-destination entries never count, per concept.md "Manual
		// edits": that is a listing/repair concern, not drift); if
		// reconciliation could not bring every one of them into place, the
		// namespace is disabled through the same path an explicit `dots
		// disable` uses, rather than left half-true.
		want := 0
		for _, e := range m.Entries {
			if e.Dest != "" {
				want++
			}
		}
		if want > 0 && len(linkedDests) != want {
			entry.LinkedDests = linkedDests
			s.Entries[key] = entry
			if disableErr := engine.Disable(key, s); disableErr != nil {
				return Findings{}, disableErr
			}
			disabled = append(disabled, Disabled{Repo: key.Repo, Namespace: key.Namespace, Reasons: nsProblems})
			changed = true
			continue
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
	sort.Slice(recovered, func(i, j int) bool {
		if recovered[i].Repo != recovered[j].Repo {
			return recovered[i].Repo < recovered[j].Repo
		}
		return recovered[i].Namespace < recovered[j].Namespace
	})
	sort.Slice(repairs, func(i, j int) bool {
		if repairs[i].Repo != repairs[j].Repo {
			return repairs[i].Repo < repairs[j].Repo
		}
		return repairs[i].Namespace < repairs[j].Namespace
	})
	sort.Slice(disabled, func(i, j int) bool {
		if disabled[i].Repo != disabled[j].Repo {
			return disabled[i].Repo < disabled[j].Repo
		}
		return disabled[i].Namespace < disabled[j].Namespace
	})
	return Findings{
		Problems:     problems,
		Unregistered: unregistered,
		Dropped:      dropped,
		Recovered:    recovered,
		NeedsRepair:  repairs,
		Disabled:     disabled,
	}, nil
}

// recoverManifest rebuilds a namespace's manifest when its .dots file is
// missing, per concept.md "Manual edits": absent is not the same evidence as
// empty, so it is never read as "every entry was cleared." It tries the
// highest-fidelity source first:
//
//  1. git's committed copy at HEAD — exact, and the only source that still
//     works for an installed-but-disabled namespace, which has no live
//     symlinks to reconstruct from.
//  2. destinations state still remembers that are still symlinks pointing
//     into this namespace's folder — proof, not inference.
//  3. a dest-less scaffold entry for whatever payload neither source
//     accounts for, which lists as "?" until a human fills in a destination
//     (namespace <ns> edit) or removes the payload.
//
// The manifest this returns is written back to disk by the caller — the one
// carve-out from "a command that reads must not write a manifest": every
// other source of an entry is a guess, but git and a live symlink are proof,
// not a guess.
func recoverManifest(repoDir, namespaceDir string, key state.Key, linkedDests []string) (manifest.Manifest, Recovery, error) {
	relPath, err := filepath.Rel(repoDir, manifest.Path(namespaceDir))
	if err != nil {
		return manifest.Manifest{}, Recovery{}, err
	}
	data, found, err := git.ShowFile(repoDir, recoveryRev, filepath.ToSlash(relPath))
	if err != nil {
		return manifest.Manifest{}, Recovery{}, err
	}
	if found {
		m, decodeErr := manifest.Decode(data)
		if decodeErr != nil {
			return manifest.Manifest{}, Recovery{}, decodeErr
		}
		return m, Recovery{Repo: key.Repo, Namespace: key.Namespace, Source: RecoveredFromGit}, nil
	}

	fromLinks := namespace.RebuildFromLinks(namespaceDir, linkedDests)
	report, err := namespace.Inspect(namespaceDir, fromLinks)
	if err != nil {
		return manifest.Manifest{}, Recovery{}, err
	}

	source := RecoveredScaffoldOnly
	if len(fromLinks) > 0 {
		source = RecoveredFromLinks
	}
	entries := append(fromLinks, namespace.Scaffold(report)...)
	return manifest.Manifest{Entries: entries}, Recovery{Repo: key.Repo, Namespace: key.Namespace, Source: source}, nil
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
		target, isSymlink, err := link.ReadIfSymlink(dest)
		if err != nil {
			problems = append(problems, Problem{Repo: key.Repo, Namespace: key.Namespace, Dest: dest, Detail: err.Error()})
			continue
		}
		if !isSymlink || !strings.HasPrefix(target, prefix) {
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
// skipStaleRemoval is true only for a namespace whose manifest was just
// recovered this pass (see recoverManifest): a rebuilt manifest may still be
// missing an entry the previous one had, and "not named by the manifest" is
// only trustworthy evidence of a real removal when the manifest itself was
// never in doubt.
func reconcileNamespace(key state.Key, namespaceDir string, entries []manifest.Entry, recorded []string, skipStaleRemoval bool) ([]string, []Problem, error) {
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
	if !skipStaleRemoval {
		for _, dest := range recorded {
			if current[dest] {
				continue
			}
			if err := removeStaleLink(dest); err != nil {
				problems = append(problems, Problem{Repo: key.Repo, Namespace: key.Namespace, Dest: dest, Detail: err.Error()})
			}
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
