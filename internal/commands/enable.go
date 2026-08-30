package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/engine"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/namespace"
	"github.com/DeprecatedLuar/dotz/internal/paths"
	"github.com/DeprecatedLuar/dotz/internal/repo"
	"github.com/DeprecatedLuar/dotz/internal/state"
	"github.com/DeprecatedLuar/dotz/internal/ui"
)

// ErrSomeSkipped is returned by a batch enable that linked at least one
// namespace but skipped at least one other, per concept.md "What enable
// reports": "Skipping is a failure of the request... the exit status is
// non-zero." The report (the "+"/"!" lines and the count line) is already
// printed by the time this is returned, so main.go exits 1 without an
// additional "Error: ..." line on top of it.
var ErrSomeSkipped = errors.New("some namespaces were skipped")

// enableNamespace implements `namespace <ns> enable` / `namespace enable
// <ns>` / `enable <ns>` for exactly one, explicitly named namespace, per
// concept.md "Enable": materialize the namespace's folder, write its state
// entry, and create its links — with every pre-flight problem collected and
// presented exactly once before anything happens. A namespace named this
// way is never silently skipped: an unresolved problem is either confirmed
// (interactively or via --force) or hard-errors. Batches of more than one
// namespace go through enableNamespaces instead, which skips rather than
// blocking the whole request.
func enableNamespace(name string, flags shared.Flags) error {
	if flags.All {
		return fmt.Errorf("usage: enable --all")
	}
	dataDir, err := paths.Data()
	if err != nil {
		return err
	}
	reg, err := manifest.ReadRegistry()
	if err != nil {
		return err
	}

	r, repoDir, nsDir, err := locateNamespaceForEnable(dataDir, reg, name, flags)
	if err != nil {
		return err
	}

	entries, _, err := engine.ManifestEntries(repoDir, nsDir, name)
	if err != nil {
		return err
	}

	key := state.Key{Repo: r.Name, Namespace: name}
	s, err := state.Read()
	if err != nil {
		return err
	}

	problems, err := engine.Preflight(key, nsDir, entries, s)
	if err != nil {
		return err
	}

	if hardBlocked(problems) {
		return fmt.Errorf("cannot enable %q:\n%s", name, renderProblems(problems))
	}

	if len(problems) > 0 {
		proceed, err := confirmProblems(name, problems, flags)
		if err != nil {
			return err
		}
		if !proceed {
			return nil
		}
	}

	if err := engine.Enable(key, repoDir, nsDir, name, entries, s, problems); err != nil {
		return err
	}
	fmt.Print(ui.Render([]ui.Entry{{Marker: ui.MarkerEnabled, Name: name}}))
	return nil
}

// enableTarget is one namespace under consideration by a batch enable, with
// its pre-flight problems filled in once the whole batch has been gathered.
type enableTarget struct {
	repo     manifest.Repo
	repoDir  string
	nsDir    string
	name     string
	display  string // repo-qualified when the bare name is ambiguous across repos
	key      state.Key
	entries  []manifest.Entry
	problems []engine.Problem
}

// enableNamespaces implements `enable <ns>...` / `enable --all` / `namespace
// enable <ns>...`: batch enable, per concept.md "Enabling more than one".
// Exactly one explicit name keeps enableNamespace's confirm-or-hard-error
// behavior; --all or more than one name runs the batch path, where a
// namespace that fails pre-flight is skipped and reported rather than
// blocking the rest.
func enableNamespaces(names []string, flags shared.Flags) error {
	if flags.All {
		if len(names) > 0 {
			return fmt.Errorf("usage: enable --all")
		}
		return runEnableBatch(nil, true, flags)
	}
	if len(names) == 0 {
		return fmt.Errorf("usage: enable <namespace>...")
	}
	if len(names) == 1 {
		return enableNamespace(names[0], flags)
	}
	return runEnableBatch(names, false, flags)
}

// runEnableBatch resolves every target namespace, runs pre-flight for the
// entire batch before the first link is created, then links every target
// that is clean or forced — skipping, never blocking, any target a
// hard-blocked problem rules out or an unresolved occupied/collision
// problem leaves unconfirmed. Per concept.md "What enable reports", it
// prints one "+"/"!" line per target and, only when something was skipped,
// a count line to stderr and a non-zero exit via ErrSomeSkipped.
func runEnableBatch(names []string, all bool, flags shared.Flags) error {
	dataDir, err := paths.Data()
	if err != nil {
		return err
	}
	reg, err := manifest.ReadRegistry()
	if err != nil {
		return err
	}
	s, err := state.Read()
	if err != nil {
		return err
	}

	var targets []enableTarget
	if all {
		targets, err = discoverAllTargets(dataDir, reg, s, flags)
	} else {
		targets, err = resolveExplicitTargets(dataDir, reg, names, flags)
	}
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return nil
	}

	for i := range targets {
		problems, err := engine.Preflight(targets[i].key, targets[i].nsDir, targets[i].entries, s)
		if err != nil {
			return err
		}
		targets[i].problems = problems
	}

	var report []ui.Entry
	var enabled, skipped int
	for _, t := range targets {
		if hardBlocked(t.problems) || (len(t.problems) > 0 && !flags.Force) {
			skipped++
			report = append(report, ui.Entry{Marker: ui.MarkerProblem, Name: t.display + "    " + problemSummaries(t.problems)})
			continue
		}
		if err := engine.Enable(t.key, t.repoDir, t.nsDir, t.name, t.entries, s, t.problems); err != nil {
			skipped++
			report = append(report, ui.Entry{Marker: ui.MarkerProblem, Name: t.display + "    " + err.Error()})
			continue
		}
		enabled++
		report = append(report, ui.Entry{Marker: ui.MarkerEnabled, Name: t.display})
	}

	fmt.Print(ui.Render(report))
	if skipped > 0 {
		fmt.Fprintf(os.Stderr, "%d enabled, %d skipped. --force to override.\n", enabled, skipped)
		return ErrSomeSkipped
	}
	return nil
}

// discoverAllTargets finds every namespace, across every registered
// repository (or the one --repo names), that is not already enabled.
func discoverAllTargets(dataDir string, reg manifest.Registry, s state.State, flags shared.Flags) ([]enableTarget, error) {
	repos := reg.Repos
	if flags.Repo != "" {
		r, err := repo.Resolve(reg.Repos, flags.Repo)
		if err != nil {
			return nil, err
		}
		repos = []manifest.Repo{r}
	}

	type candidate struct {
		repo manifest.Repo
		name string
	}
	var candidates []candidate
	counts := make(map[string]int)
	for _, r := range repos {
		names, err := allNamespaceNames(filepath.Join(dataDir, r.Name))
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			counts[name]++
			candidates = append(candidates, candidate{repo: r, name: name})
		}
	}

	var targets []enableTarget
	for _, c := range candidates {
		key := state.Key{Repo: c.repo.Name, Namespace: c.name}
		if s.Entries[key].Enabled {
			continue
		}
		repoDir := filepath.Join(dataDir, c.repo.Name)
		nsDir := filepath.Join(repoDir, c.name)
		entries, _, err := engine.ManifestEntries(repoDir, nsDir, c.name)
		if err != nil {
			return nil, err
		}
		display := c.name
		if counts[c.name] > 1 {
			display = c.repo.Name + "/" + c.name
		}
		targets = append(targets, enableTarget{
			repo: c.repo, repoDir: repoDir, nsDir: nsDir, name: c.name, display: display,
			key: key, entries: entries,
		})
	}
	return targets, nil
}

// resolveExplicitTargets locates every explicitly named namespace. An
// unresolvable name (not found, or ambiguous with no --repo and no
// terminal) is a request error, not a pre-flight problem, so it aborts
// resolution rather than being reported as a skip.
func resolveExplicitTargets(dataDir string, reg manifest.Registry, names []string, flags shared.Flags) ([]enableTarget, error) {
	targets := make([]enableTarget, 0, len(names))
	for _, name := range names {
		r, repoDir, nsDir, err := locateNamespaceForEnable(dataDir, reg, name, flags)
		if err != nil {
			return nil, err
		}
		entries, _, err := engine.ManifestEntries(repoDir, nsDir, name)
		if err != nil {
			return nil, err
		}
		targets = append(targets, enableTarget{
			repo: r, repoDir: repoDir, nsDir: nsDir, name: name, display: name,
			key: state.Key{Repo: r.Name, Namespace: name}, entries: entries,
		})
	}
	return targets, nil
}

// problemSummaries joins each problem's first line — the occupied message's
// remedy lines are omitted — into one skip reason.
func problemSummaries(problems []engine.Problem) string {
	summaries := make([]string, len(problems))
	for i, p := range problems {
		summaries[i] = strings.SplitN(p.Message, "\n", 2)[0]
	}
	return strings.Join(summaries, "; ")
}

func allNamespaceNames(repoDir string) ([]string, error) {
	local, err := namespace.LocalNames(repoDir)
	if err != nil {
		return nil, err
	}
	catalogue, err := repo.Namespaces(repoDir)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(local)+len(catalogue))
	names := make([]string, 0, len(local)+len(catalogue))
	for _, name := range append(local, catalogue...) {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// hardBlocked reports whether problems contains a kind no flag overrides:
// a protected root, the in-repo link guard, or a permission the process
// cannot escalate past.
func hardBlocked(problems []engine.Problem) bool {
	for _, p := range problems {
		if p.Kind == engine.ProtectedRoot || p.Kind == engine.LinkGuard || p.Kind == engine.Unwritable {
			return true
		}
	}
	return false
}

func renderProblems(problems []engine.Problem) string {
	lines := make([]string, len(problems))
	for i, p := range problems {
		lines[i] = p.Message
	}
	return strings.Join(lines, "\n")
}

// confirmProblems presents every resolvable pre-flight problem (occupied
// destinations, conflicting namespaces) exactly once and reports whether to
// proceed. --force proceeds without asking; otherwise an interactive prompt
// asks once for the whole list, and a non-interactive session without
// --force is a hard error naming --force as the way forward.
func confirmProblems(name string, problems []engine.Problem, flags shared.Flags) (bool, error) {
	if flags.Force {
		return true, nil
	}
	if !ui.Interactive() {
		return false, fmt.Errorf("cannot enable %q non-interactively:\n%s\nrerun with --force to proceed", name, renderProblems(problems))
	}
	choice, err := ui.Prompt(
		fmt.Sprintf("enabling %q has %d problem(s):\n%s\nproceed?", name, len(problems), renderProblems(problems)),
		[]string{"y", "N"},
	)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(choice, "y") || strings.EqualFold(choice, "yes"), nil
}

// locateNamespaceForEnable finds the namespace named name across every
// registered repository: a locally materialized folder first
// (namespace.Resolve's ordinary case), falling back to a repository's git
// catalogue for a namespace that has never been checked out on this
// machine — enable is what performs that checkout.
func locateNamespaceForEnable(dataDir string, reg manifest.Registry, name string, flags shared.Flags) (manifest.Repo, string, string, error) {
	if loc, err := namespace.Resolve(dataDir, reg.Repos, name, flags.Repo); err == nil {
		return loc.Repo, filepath.Dir(loc.Dir), loc.Dir, nil
	}

	repos := reg.Repos
	if flags.Repo != "" {
		r, err := repo.Resolve(reg.Repos, flags.Repo)
		if err != nil {
			return manifest.Repo{}, "", "", err
		}
		repos = []manifest.Repo{r}
	}

	var candidates []manifest.Repo
	for _, r := range repos {
		names, err := repo.Namespaces(filepath.Join(dataDir, r.Name))
		if err != nil {
			return manifest.Repo{}, "", "", err
		}
		for _, n := range names {
			if n == name {
				candidates = append(candidates, r)
				break
			}
		}
	}

	switch len(candidates) {
	case 0:
		return manifest.Repo{}, "", "", fmt.Errorf("no namespace named %q found in any registered repository", name)
	case 1:
		r := candidates[0]
		repoDir := filepath.Join(dataDir, r.Name)
		return r, repoDir, filepath.Join(repoDir, name), nil
	}

	repoNames := make([]string, 0, len(candidates))
	for _, r := range candidates {
		repoNames = append(repoNames, r.Name)
	}
	if !ui.Interactive() {
		return manifest.Repo{}, "", "", fmt.Errorf("namespace %q exists in multiple repositories (%s); disambiguate with --repo", name, strings.Join(repoNames, ", "))
	}
	choice, err := ui.Prompt(fmt.Sprintf("namespace %q exists in multiple repositories. Choose one:", name), repoNames)
	if err != nil {
		return manifest.Repo{}, "", "", err
	}
	for _, r := range candidates {
		if strings.EqualFold(r.Name, choice) {
			repoDir := filepath.Join(dataDir, r.Name)
			return r, repoDir, filepath.Join(repoDir, name), nil
		}
	}
	return manifest.Repo{}, "", "", fmt.Errorf("no repository named %q", choice)
}
