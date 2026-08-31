package commands

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/engine"
	"github.com/DeprecatedLuar/dotz/internal/git"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/namespace"
	"github.com/DeprecatedLuar/dotz/internal/paths"
	"github.com/DeprecatedLuar/dotz/internal/repo"
	"github.com/DeprecatedLuar/dotz/internal/state"
	"github.com/DeprecatedLuar/dotz/internal/trash"
	"github.com/DeprecatedLuar/dotz/internal/ui"
)

// Restore-time occupied-destination choices, per concept.md "Occupied
// destinations under --restore".
const (
	choiceTrash  = "t"
	choiceSkip   = "s"
	choiceCancel = "c"
)

// rmEntry implements `namespace <ns> rm <path>`, per concept.md "Removal":
// the entry leaves the manifest and its symlink comes down. By default the
// payload goes to the trash and nothing is written to the home directory;
// --restore writes it back to its destination as a real file first;
// --purge erases instead of trashing, after a successful --restore when
// both are given.
func rmEntry(name, path string, flags shared.Flags) error {
	loc, err := resolveNamespace(name, flags)
	if err != nil {
		return err
	}
	dest, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve absolute path for %s: %w", path, err)
	}

	m, err := manifest.Read(loc.Dir)
	if err != nil {
		return err
	}
	entry, ok := entryByDest(m, dest)
	if !ok {
		return fmt.Errorf("%s is not tracked in namespace %q", dest, name)
	}

	if err := confirmRemoval([]string{name}, 0, 1, flags); err != nil {
		return err
	}
	if err := removeEntries(loc, name, []manifest.Entry{entry}, flags); err != nil {
		return err
	}
	fmt.Print(ui.Report([]string{ui.Operation(ui.MarkerRemoved, entry.Name, "")}, ""))
	return nil
}

// rmNamespaces implements `namespace rm <ns>...`: every named namespace is
// disabled, its entries removed per the flags below, and its folder
// trashed. Per concept.md "Confirmation", the whole batch is confirmed once,
// naming the namespace count, the file count, and whether --restore is in
// effect.
func rmNamespaces(names []string, flags shared.Flags) error {
	type resolved struct {
		loc     namespace.Located
		entries []manifest.Entry
	}
	targets := make([]resolved, 0, len(names))
	fileCount := 0
	for _, name := range names {
		loc, err := resolveNamespace(name, flags)
		if err != nil {
			return err
		}
		m, err := manifest.Read(loc.Dir)
		if err != nil {
			return err
		}
		targets = append(targets, resolved{loc: loc, entries: m.Entries})
		fileCount += len(m.Entries)
	}

	if err := confirmRemoval(names, len(names), fileCount, flags); err != nil {
		return err
	}

	// Git safety is checked once per affected repository, before anything is
	// trashed — never inside the per-namespace loop below. Trashing a
	// namespace uses a raw filesystem move (trash.Move), which is not
	// git-aware and leaves the repo's git status dirty; checking mid-loop
	// would make an earlier namespace's own removal trip the safety check
	// for a later namespace in the same repo, self-blocking a batch the user
	// already confirmed once. `repo rm` follows this same shape.
	checkedRepos := map[string]bool{}
	for _, t := range targets {
		if checkedRepos[t.loc.Repo.Name] {
			continue
		}
		if err := checkGitSafety(t.loc.Repo.Name, filepath.Dir(t.loc.Dir), flags); err != nil {
			return err
		}
		checkedRepos[t.loc.Repo.Name] = true
	}

	// lines is printed via defer so a mid-batch failure still reports every
	// namespace already removed before the error, rather than silently
	// dropping that partial progress on an early return.
	var lines []string
	defer func() { fmt.Print(ui.Report(lines, "")) }()
	for i, t := range targets {
		if err := rmNamespaceAt(t.loc, names[i], flags); err != nil {
			return err
		}
		lines = append(lines, ui.Operation(ui.MarkerRemoved, names[i], ""))
	}
	return nil
}

// rmRepo implements `repo rm <repo>`: the same removal path as `namespace
// rm`, for every namespace in the repository, then the clone and its
// registry entry are removed.
func rmRepo(name string, flags shared.Flags) error {
	reg, err := manifest.ReadRegistry()
	if err != nil {
		return err
	}
	r, err := repo.Resolve(reg.Repos, name)
	if err != nil {
		return err
	}
	dataDir, err := paths.Data()
	if err != nil {
		return err
	}
	repoDir := filepath.Join(dataDir, r.Name)

	names, err := namespace.LocalNames(repoDir)
	if err != nil {
		return err
	}

	fileCount := 0
	entriesByName := make(map[string][]manifest.Entry, len(names))
	for _, nsName := range names {
		m, err := manifest.Read(filepath.Join(repoDir, nsName))
		if err != nil {
			return err
		}
		entriesByName[nsName] = m.Entries
		fileCount += len(m.Entries)
	}

	if err := confirmRemoval([]string{r.Name}, len(names), fileCount, flags); err != nil {
		return err
	}

	if err := checkGitSafety(r.Name, repoDir, flags); err != nil {
		return err
	}

	// lines is printed via defer so a mid-batch failure — whether a later
	// namespace's removal, the repo trash, or the registry write — still
	// reports every step already completed before the error, rather than
	// silently dropping that partial progress on an early return.
	var lines []string
	defer func() { fmt.Print(ui.Report(lines, "")) }()

	for _, nsName := range names {
		loc := namespace.Located{Repo: r, Dir: filepath.Join(repoDir, nsName)}
		if err := rmNamespaceAt(loc, nsName, flags); err != nil {
			return err
		}
		lines = append(lines, ui.Operation(ui.MarkerRemoved, nsName, ""))
	}

	if _, err := trash.Move(repoDir); err != nil {
		return fmt.Errorf("trash repository %s: %w", repoDir, err)
	}
	lines = append(lines, ui.Operation(ui.MarkerRemoved, r.Name, ""))

	remaining := make([]manifest.Repo, 0, len(reg.Repos))
	for _, existing := range reg.Repos {
		if existing.Name != r.Name {
			remaining = append(remaining, existing)
		}
	}
	reg.Repos = remaining
	if err := manifest.WriteRegistry(reg); err != nil {
		return err
	}
	return nil
}

// rmNamespaceAt removes every entry of the namespace at loc per the flags,
// clears its machine state, and trashes its folder — the shared step
// between `namespace rm <ns>...` and `repo rm <repo>`.
func rmNamespaceAt(loc namespace.Located, nsName string, flags shared.Flags) error {
	m, err := manifest.Read(loc.Dir)
	if err != nil {
		return err
	}
	if len(m.Entries) > 0 {
		if err := removeEntries(loc, nsName, m.Entries, flags); err != nil {
			return err
		}
	}
	if err := clearState(loc.Repo.Name, nsName); err != nil {
		return err
	}

	// The namespace folder itself always goes through the trash first —
	// giving Delete a recoverable rollback point exactly like every other
	// step here — and is only erased afterward under --purge.
	trashName, err := trash.Move(loc.Dir)
	if err != nil {
		return fmt.Errorf("trash namespace %s: %w", loc.Dir, err)
	}
	if flags.Purge {
		return trash.Purge(trashName)
	}
	return nil
}

// dirtyNamespaces reduces a list of git-status paths (relative to a repo
// root, so namespace/entry/...) to the sorted, deduplicated set of
// namespaces they fall under, for checkGitSafety's error to name what's
// dirty without dumping every path. rootFiles counts paths with no
// namespace segment (repo-root files such as .gitignore) separately —
// they aren't a namespace, so they're never folded into the list.
func dirtyNamespaces(paths []string) (namespaces []string, rootFiles int) {
	seen := make(map[string]bool)
	for _, p := range paths {
		head, _, ok := strings.Cut(p, "/")
		if !ok {
			rootFiles++
			continue
		}
		seen[head] = true
	}
	namespaces = make([]string, 0, len(seen))
	for ns := range seen {
		namespaces = append(namespaces, ns)
	}
	sort.Strings(namespaces)
	return namespaces, rootFiles
}

// checkGitSafety reads repoDir's git state and refuses removal when it
// would destroy work that exists nowhere else, per concept.md "Git safety
// on removal": uncommitted changes or unpushed commits point at `dots
// sync`; no remote at all warns that the trash is the only safety net.
// --force overrides all three.
func checkGitSafety(repoName, repoDir string, flags shared.Flags) error {
	if flags.Force {
		return nil
	}
	st, err := git.Status(repoDir)
	if err != nil {
		return err
	}
	if len(st.Dirty) > 0 {
		namespaces, rootFiles := dirtyNamespaces(st.Dirty)
		header := fmt.Sprintf("%q has uncommitted changes in %d namespace(s):", repoName, len(namespaces))
		if rootFiles > 0 {
			header = fmt.Sprintf("%s, plus %d repo-root file(s)", strings.TrimSuffix(header, ":"), rootFiles) + ":"
		}
		msg := ui.List(header, namespaces, "run `dots sync` first, or --force to override")
		return fmt.Errorf("%s", msg)
	}
	if st.Unpushed > 0 {
		return fmt.Errorf("%q has %d commit(s) not pushed to its remote; run `dots sync` first, or --force to override",
			repoName, st.Unpushed)
	}
	if !st.HasRemote {
		return fmt.Errorf("%q has no remote; this clone is the only copy of its content — add one with `git remote add origin <url>`, or --force to override",
			repoName)
	}
	return nil
}

// confirmRemoval implements concept.md "Confirmation": rm always confirms,
// naming the scale (namespace count and file count) and whether --restore
// is in effect, with --purge's line naming that nothing will be
// recoverable. -y (or --force, which implies it) skips the prompt.
// Non-interactively without either, rm is a hard error that changes
// nothing — checked here, before any side effect runs. namespaceCount is 0
// for a single-entry removal, which names files only.
func confirmRemoval(names []string, namespaceCount, fileCount int, flags shared.Flags) error {
	if flags.Yes {
		return nil
	}

	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = fmt.Sprintf("%q", n)
	}
	subjects := strings.Join(quoted, ", ")

	var scale string
	if namespaceCount > 0 {
		scale = fmt.Sprintf("%d namespace(s), %d file(s)", namespaceCount, fileCount)
	} else {
		scale = fmt.Sprintf("%d file(s)", fileCount)
	}

	if !ui.Interactive() {
		return fmt.Errorf("cannot remove %s (%s) non-interactively without -y", subjects, scale)
	}

	var tip string
	switch {
	case flags.Purge:
		scale += " -> ERASED FROM DISK. Not recoverable."
	case flags.Restore:
		scale += " -> restored to destination"
		tip = "--restore is in effect: files are written back to their destinations first."
	default:
		scale += " -> trash"
		tip = "Nothing is written to your home directory.\nTip: --restore puts the files back at their destinations first."
	}

	msg := ui.Confirm(fmt.Sprintf("Remove %s?", subjects), []string{scale}, tip)
	choice, err := ui.Prompt(msg, []string{"y", "N"})
	if err != nil {
		return err
	}
	if !strings.EqualFold(choice, "y") && !strings.EqualFold(choice, "yes") {
		return fmt.Errorf("removal cancelled")
	}
	return nil
}

// removeEntries removes entries from loc per the flags: --restore writes
// them back to their destinations (resolving any occupied-destination
// conflict through one confirmation for the whole batch), otherwise every
// payload goes straight to the trash. --purge erases whatever this
// operation sent to the trash once it has fully succeeded — after a
// verified --restore, never before, per concept.md "--restore --purge".
func removeEntries(loc namespace.Located, nsName string, entries []manifest.Entry, flags shared.Flags) error {
	s, err := state.Read()
	if err != nil {
		return err
	}
	key := state.Key{Repo: loc.Repo.Name, Namespace: nsName}

	// rm disables first, always, without being asked (concept.md
	// "Removal"): a namespace's destinations may hold symlinks pointing
	// into its folder, and trashing/restoring without unlinking would
	// leave them dangling.
	if err := engine.Disable(key, s); err != nil {
		return err
	}
	s, err = state.Read()
	if err != nil {
		return err
	}

	var trashed []string
	if flags.Restore {
		trashed, err = restoreEntries(loc, nsName, entries, key, s, flags)
	} else {
		trashed, err = engine.TrashEntries(key, loc.Dir, entries, s)
	}
	if err != nil {
		return err
	}

	if flags.Purge {
		for _, name := range trashed {
			if err := trash.Purge(name); err != nil {
				return err
			}
		}
	}
	return nil
}

// restoreEntries runs pre-flight for entries within loc, resolves any
// occupied-destination conflicts through one confirmation for the whole
// batch (concept.md "Occupied destinations under --restore": [t]/[s]/[c]),
// then restores every entry via engine.Restore.
func restoreEntries(loc namespace.Located, nsName string, entries []manifest.Entry, key state.Key, s state.State, flags shared.Flags) ([]string, error) {
	problems, err := engine.RestorePreflight(loc.Dir, nsName, entries)
	if err != nil {
		return nil, err
	}

	skip := false
	if len(problems) > 0 {
		if flags.Force {
			// --force resolves every occupied destination as [t]: trash the
			// occupant and restore ours.
		} else if !ui.Interactive() {
			return nil, fmt.Errorf("cannot restore %q non-interactively:\n%s\nrerun with --force to trash the occupant(s) and restore, or answer interactively",
				nsName, renderRestoreProblems(problems))
		} else {
			choice, err := ui.Prompt(
				fmt.Sprintf("restoring %q has %d occupied destination(s):\n%s\nchoose one", nsName, len(problems), renderRestoreProblems(problems)),
				[]string{choiceTrash, choiceSkip, choiceCancel},
			)
			if err != nil {
				return nil, err
			}
			switch strings.ToLower(strings.TrimSpace(choice)) {
			case choiceTrash:
				// resolved as --force above.
			case choiceSkip:
				skip = true
			default:
				return nil, fmt.Errorf("restore cancelled")
			}
		}
	}

	return engine.Restore(key, loc.Dir, entries, problems, s, skip)
}

// clearState drops a namespace's machine-state entry entirely. Used once the
// namespace itself is going away, unlike a partial `namespace <ns> rm <path>`
// which only narrows the recorded linked destinations.
func clearState(repoName, nsName string) error {
	s, err := state.Read()
	if err != nil {
		return err
	}
	key := state.Key{Repo: repoName, Namespace: nsName}
	if _, ok := s.Entries[key]; !ok {
		return nil
	}
	delete(s.Entries, key)
	return state.Write(s)
}

func renderRestoreProblems(problems []engine.RestoreProblem) string {
	lines := make([]string, len(problems))
	for i, p := range problems {
		lines[i] = p.Message
	}
	return strings.Join(lines, "\n")
}

func entryByDest(m manifest.Manifest, dest string) (manifest.Entry, bool) {
	for _, e := range m.Entries {
		if e.Dest == dest {
			return e, true
		}
	}
	return manifest.Entry{}, false
}
