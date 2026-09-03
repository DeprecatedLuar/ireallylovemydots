package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/engine"
	"github.com/DeprecatedLuar/dotz/internal/git"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/namespace"
	"github.com/DeprecatedLuar/dotz/internal/paths"
	"github.com/DeprecatedLuar/dotz/internal/profile"
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

// rmEntry implements `namespace <ns> rm <name>...`, per concept.md
// "Removal": each entry leaves the manifest and its symlink comes down. By
// default the payload goes to the trash and nothing is written to the home
// directory; --restore writes it back to its destination as a real file
// first; --purge erases instead of trashing, after a successful --restore
// when both are given.
//
// names are matched against manifest.Entry.Name — the identifier every
// listing (`dots <ns>`) already shows — not resolved as filesystem paths.
// An entry's Dest can be manifest.DestNone (deliberately never linked
// anywhere), which has no filesystem path at all, so name is the only
// identifier that works for every entry, not just linked ones.
func rmEntry(name string, names []string, flags shared.Flags) error {
	loc, err := resolveNamespace(name, flags)
	if err != nil {
		return err
	}

	m, err := manifest.Read(loc.Dir)
	if err != nil {
		return err
	}
	entries := make([]manifest.Entry, 0, len(names))
	for _, n := range names {
		entry, ok := entryByName(m, n)
		if !ok {
			return fmt.Errorf("%q is not tracked in namespace %q", n, name)
		}
		entries = append(entries, entry)
	}

	for _, entry := range entries {
		if err := clearProfileMembership(loc.Dir, name, entry.Name, flags); err != nil {
			return err
		}
	}

	rows := make([]ui.Entry, len(entries))
	for i, entry := range entries {
		rows[i] = ui.Entry{Marker: ui.MarkerMaterialized, Name: entry.Name}
	}
	if proceed, err := confirmRemoval("file", rows, flags); err != nil {
		return err
	} else if !proceed {
		return nil
	}
	if err := removeEntries(loc, name, entries, flags); err != nil {
		return err
	}
	lines := make([]string, len(entries))
	for i, entry := range entries {
		lines[i] = ui.Operation(ui.MarkerRemoved, entry.Name, "")
	}
	fmt.Print(ui.Report(lines, ""))
	return nil
}

// clearProfileMembership is the top of concept.md "Teardown"'s bottom-up
// chain: an entry declared profiled cannot be untracked while the profile
// layer still names it, so removal errors naming the step below it.
// --force collapses the chain, dropping every override to the trash and
// undeclaring the entry before the removal proceeds.
func clearProfileMembership(namespaceDir, namespaceName, entryName string, flags shared.Flags) error {
	pm, err := profile.Read(namespaceDir)
	if err != nil {
		return err
	}
	if !pm.HasEntry(entryName) {
		return nil
	}
	if !flags.Force {
		return fmt.Errorf("%q is profiled; run `dots namespace %s profiles main rm %s` first, or --force to collapse it",
			entryName, namespaceName, entryName)
	}

	holding, err := profile.OverridingProfiles(namespaceDir, entryName)
	if err != nil {
		return err
	}
	for _, p := range holding {
		if err := profile.DropOverride(namespaceDir, p, entryName); err != nil {
			return err
		}
	}
	return profile.Undeclare(namespaceDir, entryName)
}

// rmNamespaces implements `namespace rm <ns>...`: every named namespace is
// disabled, its entries removed per the flags below, and its folder
// trashed. Per concept.md "Confirmation", the whole batch is confirmed once,
// listing every namespace with its entry count.
func rmNamespaces(names []string, flags shared.Flags) error {
	type resolved struct {
		loc     namespace.Located
		entries []manifest.Entry
	}
	s, err := state.Read()
	if err != nil {
		return err
	}
	targets := make([]resolved, 0, len(names))
	rows := make([]ui.Entry, 0, len(names))
	repoNamespaces := map[string][]string{}
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
		row, err := namespaceRow(s, loc.Repo.Name, name, loc.Dir, m.Entries)
		if err != nil {
			return err
		}
		row.Count = len(m.Entries)
		rows = append(rows, row)
		repoNamespaces[loc.Repo.Name] = append(repoNamespaces[loc.Repo.Name], name)
	}

	// Git safety is checked once per affected repository, scoped to just the
	// namespace(s) being removed from it, and before the destructive
	// confirmation prompt below — refusing after the user already confirmed
	// would waste their answer. It's also never run inside the per-namespace
	// removal loop further down: trashing a namespace uses a raw filesystem
	// move (trash.Move), which is not git-aware and leaves the repo's git
	// status dirty, so checking mid-loop would make an earlier namespace's
	// own removal trip the safety check for a later namespace in the same
	// repo, self-blocking a batch the user already confirmed once. `repo rm`
	// follows this same shape.
	checkedRepos := map[string]bool{}
	for _, t := range targets {
		if checkedRepos[t.loc.Repo.Name] {
			continue
		}
		if err := checkGitSafety(t.loc.Repo.Name, filepath.Dir(t.loc.Dir), repoNamespaces[t.loc.Repo.Name], false, flags); err != nil {
			return err
		}
		checkedRepos[t.loc.Repo.Name] = true
	}

	if proceed, err := confirmRemoval("namespace", rows, flags); err != nil {
		return err
	} else if !proceed {
		return nil
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

	// Everything on disk in this repository is a removal target — "="
	// namespaces exist only in the catalogue and have no local folder to
	// trash, so they're excluded by the state filter rather than by a
	// separate check here.
	rows, err := namespaceListing(reg.Repos, listOptions{Repo: r.Name, States: onDiskStates, Counts: true})
	if err != nil {
		return err
	}
	names := make([]string, len(rows))
	for i, row := range rows {
		names[i] = row.Name
	}

	// Checked before the destructive confirmation below, per the same
	// ordering as `namespace rm`: refusing after the user already confirmed
	// would waste their answer. Unlike `namespace rm`, this passes the
	// clone's whole namespace list and keeps the repo-root-file check
	// active — a `repo rm` legitimately cares about the whole clone's state.
	if err := checkGitSafety(r.Name, repoDir, names, true, flags); err != nil {
		return err
	}

	if proceed, err := confirmRemoval("namespace", rows, flags); err != nil {
		return err
	} else if !proceed {
		return nil
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

	// Stage the removal in the index right away — see git.StagePath's doc
	// comment for why this can't wait for the next `sync`. This only
	// records the removal locally; `sync` is still the verb that commits
	// and pushes it, per concept.md "Git safety on removal": "Removal
	// never commits or pushes on the user's behalf." A repository that is
	// not actually a git repository has no index to stage anything into —
	// out of scope here exactly as it is for git.Status, which treats that
	// case as nothing to report rather than an error.
	repoDir := filepath.Dir(loc.Dir)
	if isRepo, err := repo.IsGitRepo(repoDir); err != nil {
		return err
	} else if isRepo {
		if err := git.StagePath(repoDir, nsName); err != nil {
			return err
		}
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

// namespacesIn reduces dirty to just the entries also present in scope,
// preserving dirty's order (already sorted by dirtyNamespaces).
func namespacesIn(dirty, scope []string) []string {
	inScope := make(map[string]bool, len(scope))
	for _, ns := range scope {
		inScope[ns] = true
	}
	var out []string
	for _, ns := range dirty {
		if inScope[ns] {
			out = append(out, ns)
		}
	}
	return out
}

// checkGitSafety reads repoDir's git state and refuses removal when it
// would destroy work that exists nowhere else, per concept.md "Git safety
// on removal": uncommitted changes or unpushed commits point at `dots
// sync`; no remote at all warns that the trash is the only safety net.
// --force overrides all three.
//
// The dirty-working-tree check is scoped to namespaces: only dirt inside one
// of those namespaces gates the operation, so uncommitted work in an
// unrelated namespace of the same clone never blocks it. Repository-root
// dirt (outside any namespace folder) only joins the gate when
// blockRootFiles is true — `repo rm` cares about the whole clone's state
// and sets it; `namespace rm` and `uninstall` scope to specific namespaces
// and leave it false. Unpushed commits and a missing remote are never
// scoped by namespaces — they describe the whole clone regardless of which
// namespace triggered the check.
func checkGitSafety(repoName, repoDir string, namespaces []string, blockRootFiles bool, flags shared.Flags) error {
	if flags.Force {
		return nil
	}
	st, err := git.Status(repoDir)
	if err != nil {
		return err
	}
	if len(st.Dirty) > 0 {
		dirty, rootFiles := dirtyNamespaces(st.Dirty)
		scoped := namespacesIn(dirty, namespaces)
		if !blockRootFiles {
			rootFiles = 0
		}
		if len(scoped) > 0 || rootFiles > 0 {
			header := fmt.Sprintf("%q has uncommitted changes in %d namespace(s):", repoName, len(scoped))
			if rootFiles > 0 {
				header = fmt.Sprintf("%s, plus %d repo-root file(s)", strings.TrimSuffix(header, ":"), rootFiles) + ":"
			}
			msg := ui.List(header, scoped, "run `dots sync` first, or --force to override")
			return fmt.Errorf("%s", msg)
		}
	}
	if st.Unpushed > 0 {
		return fmt.Errorf("%q has %d commit(s) not pushed to its remote; run `dots sync` first, or --force to override",
			repoName, st.Unpushed)
	}
	if !st.HasRemote {
		return fmt.Errorf("%q has no remote; this clone is the only copy of its content, add one with `git remote add origin <url>`, or --force to override",
			repoName)
	}
	return nil
}

// onDiskStates are the listing markers that mean a namespace has a local
// folder to trash — every state except "=", which exists only in a
// repository's catalogue and has nothing on disk to remove.
var onDiskStates = []string{ui.MarkerEnabled, ui.MarkerMaterialized, ui.MarkerProblem}

// confirmRemoval implements concept.md "Confirmation": rm lists what it's
// about to touch through the shared listing renderer — same markers as
// everywhere else, decorated with each target's item count — before
// touching it. noun names what targets holds ("namespace" or "file"),
// pluralized in the header alongside the summed item count. -y (or
// --force, which implies it) skips the prompt. Non-interactively without
// either, rm is a hard error that changes nothing — checked here, before
// any side effect runs. proceed is false only when the user declined at
// the prompt — not an error, since answering "no" is not a failure; err is
// reserved for an actual problem (can't prompt, can't read input).
func confirmRemoval(noun string, targets []ui.Entry, flags shared.Flags) (proceed bool, err error) {
	if flags.Yes {
		return true, nil
	}

	names := make([]string, len(targets))
	total := 0
	for i, t := range targets {
		names[i] = t.Name
		total += t.Count
	}
	// The block this feeds is printed to stderr via ui.Prompt, so its lines
	// must be coloured against that destination, not stdout.
	lines := ui.RenderLines(targets, os.Stderr)

	if !ui.Interactive() {
		return false, fmt.Errorf("cannot remove %s non-interactively without -y", strings.Join(names, ", "))
	}

	var verb, tip, warning string
	switch {
	case flags.Purge:
		verb = "purged"
		warning = "not recoverable"
	case flags.Restore:
		verb = "restored"
		tip = "Files are written back to their destinations first."
	default:
		verb = "trashed"
		tip = "If you want to restore real files before deleting, try using --restore first."
	}

	scale := ui.Plural(len(targets), noun)
	if total > 0 {
		scale = fmt.Sprintf("%s (%s)", scale, ui.Plural(total, "item"))
	}
	headerText := fmt.Sprintf("The following %s will be %s:", scale, verb)
	if warning != "" {
		headerText = fmt.Sprintf("The following %s will be %s (%s):", scale, verb, warning)
	}
	// Every removal header carries the warning tone, since rm is always a
	// destructive confirmation regardless of which flag chose the outcome.
	header := ui.WarningTone(headerText)
	block := ui.List(header, lines, tip)
	choice, err := ui.Prompt(block, "Do you want to proceed?", []string{"y", "N"})
	if err != nil {
		return false, err
	}
	if !strings.EqualFold(choice, "y") && !strings.EqualFold(choice, "yes") {
		fmt.Fprintln(os.Stderr, "\ncancelled")
		return false, nil
	}
	return true, nil
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
				fmt.Sprintf("restoring %q has %d occupied destination(s):\n%s\n", nsName, len(problems), renderRestoreProblems(problems)),
				"choose one",
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

func entryByName(m manifest.Manifest, name string) (manifest.Entry, bool) {
	for _, e := range m.Entries {
		if e.Name == name {
			return e, true
		}
	}
	return manifest.Entry{}, false
}
