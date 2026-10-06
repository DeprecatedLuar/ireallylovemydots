package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeprecatedLuar/ireallylovemydots/internal/commands/shared"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/engine"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/git"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/manifest"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/namespace"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/paths"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/repo"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/state"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/syncmode"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/trash"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/ui"
)

// Summary guidance under a repository that held a namespace on a conflict.
// %[1]s is the namespace, or a placeholder when several were held.
const (
	heldLabel          = "held"
	heldRunLine        = "  run: dots sync %[1]s --overlay | --overwrite-local | --overwrite-remote (pushes)"
	heldManualLine     = "       or edit %[1]s by hand, then: dots sync %[1]s --overwrite-remote"
	heldPlaceholder    = "<namespace>"
	heldPathIndent     = "    "
	leftAsIsLabel      = "left as-is: "
	removedLabel       = "removed upstream: "
	removalNeedsYesMsg = "would remove %d namespaces deleted upstream (%s); rerun with -y to confirm"
	removalPromptMsg   = "%q: %d namespaces were deleted upstream and will be removed here (unlinked, folders trashed):"
	removalDeclinedMsg = "removal declined; nothing applied"
	modeFlagNeedsScope = "a sync mode flag needs namespaces or --repo: dots sync <namespace> --%s"
	repoArgHint        = "%q is a repository; sync takes namespaces, so use: dots sync --repo %s"
)

// removalLimit is how many upstream-deleted namespaces one sync removes
// without confirmation.
const removalLimit = 3

// syncScope is one repository a run syncs, and which of its namespaces.
type syncScope struct {
	repo manifest.Repo
	// named is nil when every namespace is in scope.
	named map[string]bool
}

func (s syncScope) inScope(u git.Unit) bool {
	return !u.Dir || s.named == nil || s.named[u.Name]
}

// heldUnit is a namespace held on a conflict, with its conflicted paths.
type heldUnit struct {
	name  string
	paths []string
}

// syncResult is one repository's outcome, held for the end-of-run summary.
type syncResult struct {
	name     string
	err      error
	held     []heldUnit
	leftAsIs []string
	removed  []string
}

// HandleSync implements `dots sync [<ns>...] [--repo <repo>] [--<mode>]`:
// every registered repository, one (--repo), or only the named namespaces.
// Every repository is attempted; a failure or a conflict-held namespace
// makes the run exit non-zero via ErrSomeSkipped after the summary prints.
func HandleSync(args []string, flags shared.Flags) error {
	var flagMode syncmode.Mode
	if flags.SyncMode != "" {
		m, err := syncmode.Parse(flags.SyncMode)
		if err != nil {
			return err
		}
		if len(args) == 0 && flags.Repo == "" {
			return fmt.Errorf(modeFlagNeedsScope, m)
		}
		flagMode = m
	}

	reg, err := manifest.ReadRegistry()
	if err != nil {
		return err
	}
	dataDir, err := paths.Data()
	if err != nil {
		return err
	}
	scopes, err := syncScopes(dataDir, reg.Repos, args, flags.Repo)
	if err != nil {
		return err
	}
	access, err := state.ReadAccess()
	if err != nil {
		return err
	}
	if flagMode == syncmode.OverwriteRemote {
		for _, sc := range scopes {
			if access.IsReadOnly(sc.repo.Name) {
				return fmt.Errorf("%q: %w", sc.repo.Name, syncmode.ErrReadOnlyOverwriteRemote)
			}
		}
	}

	results := make([]syncResult, 0, len(scopes))
	for _, sc := range scopes {
		fmt.Printf("\n%s\n", sc.repo.Name)
		results = append(results, syncRepo(dataDir, sc, flagMode, access.IsReadOnly(sc.repo.Name), flags))
	}
	printSyncSummary(results)

	for _, res := range results {
		if res.err != nil || len(res.held) > 0 {
			return ErrSomeSkipped
		}
	}
	return nil
}

// syncScopes resolves sync's arguments: no arguments is every repository
// (or --repo's one) whole; otherwise each argument is a namespace, bare or
// repo-qualified, grouped by its repository.
func syncScopes(dataDir string, repos []manifest.Repo, args []string, repoSpec string) ([]syncScope, error) {
	if len(args) == 0 {
		if repoSpec == "" {
			scopes := make([]syncScope, len(repos))
			for i, r := range repos {
				scopes[i] = syncScope{repo: r}
			}
			return scopes, nil
		}
		r, err := repo.Resolve(repos, repoSpec)
		if err != nil {
			return nil, err
		}
		return []syncScope{{repo: r}}, nil
	}

	var scopes []syncScope
	index := map[string]int{}
	for _, arg := range args {
		r, ns, err := resolveSyncArg(dataDir, repos, arg, repoSpec)
		if err != nil {
			return nil, err
		}
		i, ok := index[r.Name]
		if !ok {
			i = len(scopes)
			index[r.Name] = i
			scopes = append(scopes, syncScope{repo: r, named: map[string]bool{}})
		}
		scopes[i].named[ns] = true
	}
	return scopes, nil
}

// resolveSyncArg resolves one sync argument to its repository and
// namespace. A bare repository name is refused, pointing at --repo.
func resolveSyncArg(dataDir string, repos []manifest.Repo, arg, repoSpec string) (manifest.Repo, string, error) {
	loc, err := namespace.Resolve(dataDir, repos, arg, repoSpec)
	if err == nil {
		return loc.Repo, loc.Name, nil
	}
	if r, repoErr := repo.Resolve(repos, arg); repoErr == nil {
		return manifest.Repo{}, "", fmt.Errorf(repoArgHint, arg, r.Name)
	}
	return manifest.Repo{}, "", err
}

// syncRepo runs one repository: rewind removed namespaces (writable only),
// prepare, decide each entry, trash what overwrite-local discards, apply,
// record held bases, reapply the cone, then push.
func syncRepo(dataDir string, sc syncScope, flagMode syncmode.Mode, readOnly bool, flags shared.Flags) syncResult {
	name := sc.repo.Name
	repoDir := filepath.Join(dataDir, name)
	res := syncResult{name: name}

	if !readOnly {
		if err := rewindRemovedNamespaces(repoDir, name, flags); err != nil {
			res.err = err
			return res
		}
	}

	s, err := state.Read()
	if err != nil {
		res.err = err
		return res
	}
	p, err := git.Prepare(repoDir, heldBases(s, name))
	if err != nil {
		res.err = err
		return res
	}

	placements := map[string]git.Placement{}
	held := map[string]bool{}
	var trashed []string
	for _, u := range p.Units {
		out, err := decideUnit(s, name, sc, u, flagMode, readOnly)
		if err != nil {
			res.err = err
			return res
		}
		placements[u.Name] = out.Placement
		if u.Dir && u.RemovedRemote && out.Placement.Worktree == git.SourceRemote {
			if installedHere(s, repoDir, name, u.Name) {
				res.removed = append(res.removed, u.Name)
			}
			continue
		}
		switch {
		case out.Trash:
			trashed = append(trashed, u.Name)
		case out.Held && len(u.Conflicts) > 0:
			held[u.Name] = true
			res.held = append(res.held, heldUnit{name: u.Name, paths: u.Conflicts})
		case out.Held:
			held[u.Name] = true
			res.leftAsIs = append(res.leftAsIs, u.Name)
		}
	}

	if err := confirmUpstreamRemoval(name, res.removed, flags); err != nil {
		res.err = err
		res.removed = nil
		return res
	}

	if err := trashNamespaces(repoDir, trashed); err != nil {
		res.err = err
		return res
	}
	if err := dropRemovedNamespaces(repoDir, name, res.removed); err != nil {
		res.err = err
		return res
	}
	pushable, err := git.Finish(repoDir, p, placements, !readOnly)
	if err != nil {
		res.err = err
		return res
	}
	if err := repo.Reapply(repoDir); err != nil {
		res.err = err
		return res
	}
	if err := recordHeldBases(name, p.Units, held); err != nil {
		res.err = err
		return res
	}

	if pushable && !readOnly {
		if err := git.Push(repoDir); err != nil {
			if errors.Is(err, git.ErrPushAuthFailed) {
				recordReadOnly(name, true)
			}
			res.err = err
			return res
		}
		recordReadOnly(name, false)
	}
	return res
}

// decideUnit resolves one entry's mode and placement. The mode is only
// resolved for an in-scope entry with local changes, so a saved mode a
// read-only repository refuses errors only when it would act.
func decideUnit(s state.State, repoName string, sc syncScope, u git.Unit, flagMode syncmode.Mode, readOnly bool) (syncmode.Outcome, error) {
	unit := syncmode.Unit{
		Dir:           u.Dir,
		InScope:       sc.inScope(u),
		ChangedLocal:  u.ChangedLocal,
		Conflicted:    len(u.Conflicts) > 0,
		RemovedRemote: u.RemovedRemote,
	}
	if unit.ChangedLocal && unit.InScope {
		var flag, saved syncmode.Mode
		if u.Dir {
			flag = flagMode
			saved = syncmode.Mode(s.Entries[state.Key{Repo: repoName, Namespace: u.Name}].SyncMode)
		}
		mode, err := syncmode.Effective(flag, saved, readOnly)
		if err != nil {
			return syncmode.Outcome{}, fmt.Errorf("%s: %w", u.Name, err)
		}
		unit.Mode = mode
	}
	out, err := syncmode.Decide(unit)
	if errors.Is(err, syncmode.ErrRootConflict) {
		return out, fmt.Errorf("%s %w; resolve it by hand in the clone", strings.Join(u.Conflicts, ", "), err)
	}
	return out, err
}

// confirmUpstreamRemoval asks before one sync removes more than removalLimit
// namespaces another machine deleted; -y skips the prompt.
func confirmUpstreamRemoval(repoName string, removed []string, flags shared.Flags) error {
	if len(removed) <= removalLimit || flags.Yes {
		return nil
	}
	if !ui.Interactive() {
		return fmt.Errorf(removalNeedsYesMsg, len(removed), strings.Join(removed, ", "))
	}
	block := ui.List(ui.WarningTone(fmt.Sprintf(removalPromptMsg, repoName, len(removed))), removed, "")
	choice, err := ui.Prompt(block, "Do you want to proceed?", []string{"y", "N"})
	if err != nil {
		return err
	}
	if !ui.IsYes(choice) {
		return errors.New(removalDeclinedMsg)
	}
	return nil
}

// installedHere reports whether a namespace has a state entry or a folder
// on this machine.
func installedHere(s state.State, repoDir, repoName, ns string) bool {
	if _, ok := s.Entries[state.Key{Repo: repoName, Namespace: ns}]; ok {
		return true
	}
	_, err := os.Lstat(filepath.Join(repoDir, ns))
	return err == nil
}

// dropRemovedNamespaces does on this machine what namespace rm did on the
// one that deleted each namespace: unlink it, forget it, trash its folder.
func dropRemovedNamespaces(repoDir, repoName string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	s, err := state.Read()
	if err != nil {
		return err
	}
	for _, n := range names {
		key := state.Key{Repo: repoName, Namespace: n}
		if err := engine.Disable(key, s); err != nil {
			return err
		}
		delete(s.Entries, key)
		dir := filepath.Join(repoDir, n)
		if _, err := os.Lstat(dir); err == nil {
			if _, err := trash.Move(dir); err != nil {
				return fmt.Errorf("trash %s: %w", dir, err)
			}
		}
	}
	return state.Write(s)
}

// heldBases returns the saved held base of every namespace of repoName.
func heldBases(s state.State, repoName string) map[string]string {
	bases := map[string]string{}
	for k, e := range s.Entries {
		if k.Repo == repoName && e.HeldBase != "" {
			bases[k.Namespace] = e.HeldBase
		}
	}
	return bases
}

// recordHeldBases saves each held namespace's base, keeping one already
// saved, and clears it for every namespace no longer held. Entries are
// never created just to record nothing.
func recordHeldBases(repoName string, units []git.Unit, held map[string]bool) error {
	s, err := state.Read()
	if err != nil {
		return err
	}
	changed := false
	for _, u := range units {
		if !u.Dir {
			continue
		}
		key := state.Key{Repo: repoName, Namespace: u.Name}
		e, ok := s.Entries[key]
		want := ""
		if held[u.Name] {
			want = e.HeldBase
			if want == "" {
				want = u.Base
			}
		}
		if (!ok && want == "") || e.HeldBase == want {
			continue
		}
		e.HeldBase = want
		s.Entries[key] = e
		changed = true
	}
	if !changed {
		return nil
	}
	return state.Write(s)
}

// trashNamespaces sends every dirty path inside the named namespaces to
// the trash.
func trashNamespaces(repoDir string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	status, err := git.Status(repoDir)
	if err != nil {
		return err
	}
	var dirty []string
	for _, path := range status.Dirty {
		for _, ns := range names {
			if strings.HasPrefix(path, ns+"/") {
				dirty = append(dirty, path)
				break
			}
		}
	}
	return discardDirtyPaths(repoDir, dirty)
}

// printSyncSummary renders one line per repository: "!" for a failure or
// a conflict-held namespace, "+" otherwise, noting namespaces removed
// upstream and left as-is.
func printSyncSummary(results []syncResult) {
	lines := make([]string, 0, len(results))
	for _, res := range results {
		if res.err != nil {
			lines = append(lines, ui.Operation(ui.MarkerProblem, res.name, res.err.Error()))
			continue
		}
		var parts []string
		if len(res.removed) > 0 {
			parts = append(parts, removedLabel+strings.Join(res.removed, ", "))
		}
		if len(res.leftAsIs) > 0 {
			parts = append(parts, leftAsIsLabel+strings.Join(res.leftAsIs, ", "))
		}
		detail := strings.Join(parts, "; ")
		if len(res.held) > 0 {
			if detail != "" {
				detail += "; "
			}
			lines = append(lines, ui.Operation(ui.MarkerProblem, res.name, detail+heldDetail(res.held)))
			continue
		}
		lines = append(lines, ui.Operation(ui.MarkerEnabled, res.name, detail))
	}
	fmt.Print(ui.Report(lines, ""))
}

// heldDetail lists each conflict-held namespace with its paths, then the
// commands that settle it.
func heldDetail(held []heldUnit) string {
	var b strings.Builder
	b.WriteString(heldLabel)
	for _, h := range held {
		paths := make([]string, len(h.paths))
		for i, p := range h.paths {
			paths[i] = strings.TrimPrefix(p, h.name+"/")
		}
		fmt.Fprintf(&b, "\n%s%s: %s", heldPathIndent, h.name, strings.Join(paths, ", "))
	}
	target := heldPlaceholder
	if len(held) == 1 {
		target = held[0].name
	}
	fmt.Fprintf(&b, "\n"+heldRunLine+"\n"+heldManualLine, target)
	return b.String()
}

// discardDirtyPaths moves every dirty path in repoDir into the trash, per
// concept.md "Read-only repositories": "Discard routes through the trash
// like every other destructive path in dots, never through `git checkout`
// or `git reset --hard`." A path already gone from disk (the source half of
// a rename, or one a sibling entry under the same directory already carried
// away) is not an error — there is nothing left there to discard.
func discardDirtyPaths(repoDir string, dirty []string) error {
	for _, path := range dirty {
		abs := filepath.Join(repoDir, path)
		if _, err := os.Lstat(abs); os.IsNotExist(err) {
			continue
		}
		if _, err := trash.Move(abs); err != nil {
			return fmt.Errorf("discard %s in %s: %w", path, repoDir, err)
		}
	}
	return nil
}

// recordReadOnly persists repoName's read-only flag, per concept.md
// "Read-only repositories": "the outcome of a real push is authoritative."
// A failure to persist is reported but not fatal to the sync itself — sync
// already knows and reports its own outcome; only this machine-local
// bookkeeping is at risk, healed the next time a push's outcome disagrees
// with it.
func recordReadOnly(repoName string, readOnly bool) {
	access, err := state.ReadAccess()
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.WarningTone(fmt.Sprintf("! %s%scould not record read-only access: %v", repoName, ui.DetailSep, err)))
		return
	}
	access.SetReadOnly(repoName, readOnly)
	if err := state.WriteAccess(access); err != nil {
		fmt.Fprintln(os.Stderr, ui.WarningTone(fmt.Sprintf("! %s%scould not record read-only access: %v", repoName, ui.DetailSep, err)))
	}
}

// rewindRemovedNamespaces closes the gap `rm` leaves open: staging a
// namespace's deletion (git.StagePath, at trash time) records that it's
// gone going forward, but its content can still sit in commits already on
// HEAD. Left alone, the next commit would simply add a deletion on top,
// and any such content would still reach the remote on push. When every
// commit touching a removed namespace is still unpushed, this rewinds
// past them and recommits the current tree instead, so that content never
// reaches the remote at all. Content already pushed cannot be
// unpublished this way — that's reported, not rewound, and sync proceeds
// with the ordinary deletion commit.
func rewindRemovedNamespaces(repoDir, repoName string, flags shared.Flags) error {
	removed, err := git.StagedRemovals(repoDir)
	if err != nil {
		return err
	}
	if len(removed) == 0 {
		return nil
	}

	pushed, err := git.PushedCommitsTouching(repoDir, removed)
	if err != nil {
		return err
	}
	if pushed > 0 {
		fmt.Fprintf(os.Stderr, "%q: %s already pushed; removing it locally cannot unpublish it — rotate any credential it held\n",
			repoName, strings.Join(removed, ", "))
		return nil
	}

	unpushed, err := git.UnpushedCommitsTouching(repoDir, removed)
	if err != nil {
		return err
	}
	if len(unpushed) == 0 {
		return nil
	}

	if !flags.Yes {
		if !ui.Interactive() {
			return fmt.Errorf("%q has %s removed but still only in unpushed commit(s); rerun with -y to rewind them out of history before they're pushed, or without to leave the deletion commit as-is",
				repoName, strings.Join(removed, ", "))
		}
		block := ui.List(
			ui.WarningTone(fmt.Sprintf("%q: %s only exists in %d unpushed commit(s), which will be rewound out of history so its content never reaches the remote:", repoName, strings.Join(removed, ", "), len(unpushed))),
			unpushed,
			"This collapses those commits into one; nothing pushed is touched.",
		)
		choice, err := ui.Prompt(block, "Do you want to proceed?", []string{"y", "N"})
		if err != nil {
			return err
		}
		if !ui.IsYes(choice) {
			fmt.Fprintln(os.Stderr, "\nrewind skipped; the deletion will commit normally")
			return nil
		}
	}

	target, err := git.RewindTarget(repoDir, removed)
	if err != nil {
		return err
	}
	return git.RewindAndRecommit(repoDir, target)
}
