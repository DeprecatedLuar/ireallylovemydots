package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/git"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/paths"
	"github.com/DeprecatedLuar/dotz/internal/repo"
	"github.com/DeprecatedLuar/dotz/internal/state"
	"github.com/DeprecatedLuar/dotz/internal/trash"
	"github.com/DeprecatedLuar/dotz/internal/ui"
)

// dirtyReadOnlyTip names the escape hatch for a dirty read-only repository's
// local edits, printed on both the skip and discard resolutions. Phase 16
// adds the `cp` command it names; the tip text is written now, per
// concept.md "Read-only repositories".
const dirtyReadOnlyTip = "dots cp <repo>/<namespace> <yourrepo>/<namespace>, then push the copy from there"

// readOnlySkippedDetail is the summary detail for a dirty read-only
// repository resolved to skip, per concept.md "Read-only repositories":
// "reported as skipped and behind its remote" — it is left with edits that
// were never fetched against, so both facts are named.
const readOnlySkippedDetail = "skipped: read-only with local edits sync cannot fast-forward over; left unfetched, possibly behind the remote"

// sideNeedsRepoMessage is the error for --local/--remote given with no
// repository named.
const sideNeedsRepoMessage = "--local/--remote require naming the repository: dots sync <repo> --local"

// sideNames spells each git.Side the way the summary reports it.
var sideNames = map[git.Side]string{
	git.SideLocal:  "local",
	git.SideRemote: "remote",
}

// conflictRunLines and conflictManualLines are the summary's guidance under
// a conflicted repository, per concept.md "Resolving a conflict". Their
// indexed verbs are: 1 repository name, 2 clone directory, 3 remote name,
// 4 branch.
const (
	conflictRemoteName = "origin"
	conflictRunLines   = "  run: dots sync %[1]s --local   (keep this machine)\n" +
		"       dots sync %[1]s --remote  (keep remote)"
	conflictManualLines = "  cannot be auto-resolved (edited on one side, deleted or renamed on the other):\n" +
		"    cd %[2]s && git rebase %[3]s/%[4]s\n" +
		"    resolve, then: dots sync %[1]s"
	conflictPathIndent = "    "
	conflictLabel      = "conflict"
)

// syncResult is one repository's sync outcome, held until every named
// repository has been attempted so the summary prints once at the end,
// per concept.md "Sync": "Each repository prints a header and streams its
// git output live beneath it; the summary of all results, successes and
// failures together, prints once at the end."
type syncResult struct {
	name string
	err  error
	// detail annotates a non-error outcome, such as a dirty read-only
	// repository left untouched by a skip resolution.
	detail string
}

// HandleSync implements `dots sync [repo...]`, per concept.md "Sync": one
// indivisible commit-fetch-rebase-reapply-push per repository, run across
// every registered repository, or only the ones named as arguments — sync
// is the one verb whose arguments are already repository names, so there
// is nothing for --repo to disambiguate. Partial failure is expected: a
// repository that stops on a genuine divergence is reported failed and
// left unpushed, but every other named repository still syncs, per
// concept.md: "If the third of five repositories stops with a divergence,
// the other four still sync." ErrSomeSkipped signals the non-zero exit
// without repeating the summary this already printed.
func HandleSync(args []string, flags shared.Flags) error {
	reg, err := manifest.ReadRegistry()
	if err != nil {
		return err
	}

	targets, err := syncTargets(reg.Repos, args)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return nil
	}

	dataDir, err := paths.Data()
	if err != nil {
		return err
	}

	side, err := resolveConflictSide(targets, args, flags)
	if err != nil {
		return err
	}

	discard, err := resolveDirtyReadOnly(dataDir, targets, flags)
	if err != nil {
		return err
	}

	results := make([]syncResult, 0, len(targets))
	for _, r := range targets {
		fmt.Printf("\n%s\n", r.Name)
		results = append(results, syncRepo(dataDir, r.Name, flags, discard, side))
	}

	printSyncSummary(results)

	for _, res := range results {
		if res.err != nil {
			return ErrSomeSkipped
		}
	}
	return nil
}

// syncTargets resolves sync's positional arguments into the repositories
// to run against: every registered repository with none given, or exactly
// the ones named, each resolved the same way a bare repository name
// resolves elsewhere.
func syncTargets(repos []manifest.Repo, args []string) ([]manifest.Repo, error) {
	if len(args) == 0 {
		return repos, nil
	}
	targets := make([]manifest.Repo, 0, len(args))
	for _, name := range args {
		r, err := repo.Resolve(repos, name)
		if err != nil {
			return nil, err
		}
		targets = append(targets, r)
	}
	return targets, nil
}

// syncRepo runs one repository's full sync cycle: rewind any removed
// namespace still reachable only from unpushed history, reconcile (commit,
// fetch, rebase), reapply the sparse cone a rebase may have widened, then
// push — in that order, per concept.md "Sync": "Sync runs `git
// sparse-checkout reapply` after rebasing and before pushing... Pushing
// before it would publish exactly the deletions it exists to prevent." A
// repository with no remote reconciles (commits locally) and stops there
// cleanly, per concept.md: "A repository with no remote... commits
// locally, reports that it has nothing to fetch or push, and is not a
// failure."
func syncRepo(dataDir, name string, flags shared.Flags, discard map[string]bool, side git.Side) syncResult {
	repoDir := filepath.Join(dataDir, name)

	access, err := state.ReadAccess()
	if err != nil {
		return syncResult{name: name, err: err}
	}
	if access.IsReadOnly(name) {
		return syncReadOnlyRepo(repoDir, name, discard[name])
	}

	if err := rewindRemovedNamespaces(repoDir, name, flags); err != nil {
		return syncResult{name: name, err: err}
	}

	reconciled, err := git.Reconcile(repoDir, side)
	if err != nil {
		return syncResult{name: name, err: err}
	}
	if !reconciled.RemoteConfigured {
		return syncResult{name: name}
	}

	if err := repo.Reapply(repoDir); err != nil {
		return syncResult{name: name, err: err}
	}
	if err := git.Push(repoDir); err != nil {
		if errors.Is(err, git.ErrPushAuthFailed) {
			recordReadOnly(name, true)
		}
		return syncResult{name: name, err: err}
	}
	recordReadOnly(name, false)
	return syncResult{name: name, detail: overriddenDetail(side, reconciled.Overridden)}
}

// overriddenDetail is the summary detail for a conflict a side flag settled,
// per concept.md "Resolving a conflict": "an override is never silent."
func overriddenDetail(side git.Side, overridden []string) string {
	if len(overridden) == 0 {
		return ""
	}
	return fmt.Sprintf("kept %s: %s", sideNames[side], strings.Join(overridden, ", "))
}

// resolveConflictSide validates --local/--remote and maps them to a
// git.Side, per concept.md "Resolving a conflict". Nothing syncs on error:
// both flags together are contradictory, a bare `dots sync --local` would
// override conflicts in every repository at once, and read-only repositories
// never rebase so have no conflict to settle.
func resolveConflictSide(targets []manifest.Repo, args []string, flags shared.Flags) (git.Side, error) {
	if !flags.Local && !flags.Remote {
		return git.SideNone, nil
	}
	if flags.Local && flags.Remote {
		return git.SideNone, errors.New("--local and --remote are contradictory; pick one")
	}
	if len(args) == 0 {
		return git.SideNone, errors.New(sideNeedsRepoMessage)
	}

	access, err := state.ReadAccess()
	if err != nil {
		return git.SideNone, err
	}
	for _, r := range targets {
		if access.IsReadOnly(r.Name) {
			return git.SideNone, fmt.Errorf("%q is read-only on this machine and never rebases; --local/--remote do not apply", r.Name)
		}
	}

	if flags.Local {
		return git.SideLocal, nil
	}
	return git.SideRemote, nil
}

// syncReadOnlyRepo runs the fetch-only sync path for a repository this
// machine cannot push to, per concept.md "Read-only repositories": "fetch,
// then fast-forward. It never commits, never rebases, never pushes, and
// never rewinds." A dirty repository's resolution (skip or discard) was
// already decided in HandleSync's preflight pass (resolveDirtyReadOnly),
// named by doDiscard.
func syncReadOnlyRepo(repoDir, name string, doDiscard bool) syncResult {
	status, err := git.Status(repoDir)
	if err != nil {
		return syncResult{name: name, err: err}
	}

	if len(status.Dirty) > 0 {
		if !doDiscard {
			return syncResult{name: name, detail: readOnlySkippedDetail}
		}
		if err := discardDirtyPaths(repoDir, status.Dirty); err != nil {
			return syncResult{name: name, err: err}
		}
	}

	if _, err := git.FastForward(repoDir); err != nil {
		return syncResult{name: name, err: err}
	}
	if err := repo.Reapply(repoDir); err != nil {
		return syncResult{name: name, err: err}
	}
	return syncResult{name: name}
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

// resolveDirtyReadOnly collects every dirty read-only repository among
// targets and resolves each one — skip or discard — before the per-
// repository loop starts streaming git output, per concept.md "Read-only
// repositories": "Every dirty read-only repository is collected in one pass
// before any repository starts streaming git output, and each is resolved
// there, not mid-stream." The returned map names only the repositories
// resolved to discard; every other repository (not read-only, not dirty, or
// resolved to skip) is absent from it.
func resolveDirtyReadOnly(dataDir string, targets []manifest.Repo, flags shared.Flags) (map[string]bool, error) {
	access, err := state.ReadAccess()
	if err != nil {
		return nil, err
	}

	discard := map[string]bool{}
	for _, r := range targets {
		if !access.IsReadOnly(r.Name) {
			continue
		}
		status, err := git.Status(filepath.Join(dataDir, r.Name))
		if err != nil {
			return nil, err
		}
		if len(status.Dirty) == 0 {
			continue
		}

		doDiscard, err := resolveDirtyReadOnlyChoice(r.Name, status.Dirty, flags)
		if err != nil {
			return nil, err
		}
		if doDiscard {
			discard[r.Name] = true
		}
	}
	return discard, nil
}

// resolveDirtyReadOnlyChoice picks skip or discard for one dirty read-only
// repository. `--discard` and `--yes` decide it without a prompt — in that
// order, since `--discard` is the more specific ask — and `--force` does
// not: per concept.md "Read-only repositories", "`--force` does not select
// discard... it gets its own flag so it is never reached by accident." A
// non-interactive session with neither flag defaults to skip, "because it
// is the one that loses nothing."
func resolveDirtyReadOnlyChoice(name string, dirty []string, flags shared.Flags) (doDiscard bool, err error) {
	if flags.Discard {
		return true, nil
	}
	if flags.Yes {
		return false, nil
	}
	if !ui.Interactive() {
		return false, nil
	}

	block := ui.List(
		ui.WarningTone(fmt.Sprintf("%q is read-only on this machine and has local edits sync cannot fast-forward over:", name)),
		dirty,
		dirtyReadOnlyTip,
	)
	choice, err := ui.Prompt(block, "Skip these edits, or discard them?", []string{"skip", "discard"})
	if err != nil {
		return false, err
	}
	return strings.EqualFold(choice, "discard"), nil
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

// printSyncSummary renders one line per repository sync attempted, in the
// listing alphabet: "+" for a repository that reconciled cleanly (pushed
// or not, since a repository with no remote is not a failure), "!" for
// one that stopped.
func printSyncSummary(results []syncResult) {
	lines := make([]string, 0, len(results))
	for _, res := range results {
		var conflict *git.ConflictError
		if errors.As(res.err, &conflict) {
			lines = append(lines, ui.Operation(ui.MarkerProblem, res.name, conflictDetail(res.name, conflict)))
			continue
		}
		if res.err != nil {
			lines = append(lines, ui.Operation(ui.MarkerProblem, res.name, res.err.Error()))
			continue
		}
		lines = append(lines, ui.Operation(ui.MarkerEnabled, res.name, res.detail))
	}
	fmt.Print(ui.Report(lines, ""))
}

// conflictDetail renders a conflicted repository's summary detail: the label,
// every conflicted path one per line, then either the exact commands that
// resolve it or, when a side flag cannot settle it, the manual steps.
func conflictDetail(name string, c *git.ConflictError) string {
	var b strings.Builder
	b.WriteString(conflictLabel)
	for _, p := range c.Paths {
		b.WriteString("\n" + conflictPathIndent + p)
	}
	if c.Resolvable {
		fmt.Fprintf(&b, "\n"+conflictRunLines, name)
	} else {
		fmt.Fprintf(&b, "\n"+conflictManualLines, name, c.Dir, conflictRemoteName, c.Branch)
	}
	return b.String()
}
