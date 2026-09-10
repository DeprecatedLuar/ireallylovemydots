package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/git"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/paths"
	"github.com/DeprecatedLuar/dotz/internal/repo"
	"github.com/DeprecatedLuar/dotz/internal/ui"
)

// syncResult is one repository's sync outcome, held until every named
// repository has been attempted so the summary prints once at the end,
// per concept.md "Sync": "Each repository prints a header and streams its
// git output live beneath it; the summary of all results, successes and
// failures together, prints once at the end."
type syncResult struct {
	name string
	err  error
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

	results := make([]syncResult, 0, len(targets))
	for _, r := range targets {
		fmt.Printf("\n%s\n", r.Name)
		results = append(results, syncRepo(dataDir, r.Name, flags))
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
func syncRepo(dataDir, name string, flags shared.Flags) syncResult {
	repoDir := filepath.Join(dataDir, name)

	if err := rewindRemovedNamespaces(repoDir, name, flags); err != nil {
		return syncResult{name: name, err: err}
	}

	hasRemote, err := git.Reconcile(repoDir)
	if err != nil {
		return syncResult{name: name, err: err}
	}
	if !hasRemote {
		return syncResult{name: name}
	}

	if err := repo.Reapply(repoDir); err != nil {
		return syncResult{name: name, err: err}
	}
	if err := git.Push(repoDir); err != nil {
		return syncResult{name: name, err: err}
	}
	return syncResult{name: name}
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
		if res.err != nil {
			lines = append(lines, ui.Operation(ui.MarkerProblem, res.name, res.err.Error()))
			continue
		}
		lines = append(lines, ui.Operation(ui.MarkerEnabled, res.name, ""))
	}
	fmt.Print(ui.Report(lines, ""))
}
