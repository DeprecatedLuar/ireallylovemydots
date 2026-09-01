package commands

import (
	"fmt"
	"path/filepath"

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
func HandleSync(args []string) error {
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
		results = append(results, syncRepo(dataDir, r.Name))
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

// syncRepo runs one repository's full sync cycle: reconcile (commit,
// fetch, rebase), reapply the sparse cone a rebase may have widened, then
// push — in that order, per concept.md "Sync": "Sync runs `git
// sparse-checkout reapply` after rebasing and before pushing... Pushing
// before it would publish exactly the deletions it exists to prevent." A
// repository with no remote reconciles (commits locally) and stops there
// cleanly, per concept.md: "A repository with no remote... commits
// locally, reports that it has nothing to fetch or push, and is not a
// failure."
func syncRepo(dataDir, name string) syncResult {
	repoDir := filepath.Join(dataDir, name)

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
