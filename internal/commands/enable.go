package commands

import (
	"fmt"
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

// enableNamespace implements `namespace <ns> enable` / `namespace enable
// <ns>` / `enable <ns>`, per concept.md "Enable": materialize the
// namespace's folder, write its state entry, and create its links — with
// every pre-flight problem collected and presented exactly once before
// anything happens.
func enableNamespace(name string, flags shared.Flags) error {
	if flags.All {
		if name != "" {
			return fmt.Errorf("usage: enable --all")
		}
		return enableAll(flags)
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

	return engine.Enable(key, repoDir, nsDir, name, entries, s, problems)
}

type enableTarget struct {
	repo     manifest.Repo
	repoDir  string
	nsDir    string
	name     string
	key      state.Key
	entries  []manifest.Entry
	problems []engine.Problem
}

// enableAll implements `enable --all`: discover every namespace, run every
// pre-flight check before changing anything, show one preview, prompt once,
// then execute the same engine operation used by single-namespace enable.
func enableAll(flags shared.Flags) error {
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

	repos := reg.Repos
	if flags.Repo != "" {
		r, err := repo.Resolve(reg.Repos, flags.Repo)
		if err != nil {
			return err
		}
		repos = []manifest.Repo{r}
	}

	var targets []enableTarget
	namespaceCounts := make(map[string]int)
	for _, r := range repos {
		repoDir := filepath.Join(dataDir, r.Name)
		names, err := allNamespaceNames(repoDir)
		if err != nil {
			return err
		}
		for _, name := range names {
			namespaceCounts[name]++
			key := state.Key{Repo: r.Name, Namespace: name}
			if s.Entries[key].Enabled {
				continue
			}
			nsDir := filepath.Join(repoDir, name)
			entries, _, err := engine.ManifestEntries(repoDir, nsDir, name)
			if err != nil {
				return err
			}
			problems, err := engine.Preflight(key, nsDir, entries, s)
			if err != nil {
				return err
			}
			targets = append(targets, enableTarget{
				repo: r, repoDir: repoDir, nsDir: nsDir, name: name,
				key: key, entries: entries, problems: problems,
			})
		}
	}

	if len(targets) == 0 {
		return nil
	}

	preview := make([]ui.Entry, 0, len(targets))
	var problems []engine.Problem
	for _, target := range targets {
		name := target.name
		if namespaceCounts[name] > 1 {
			name = target.repo.Name + "/" + name
		}
		preview = append(preview, ui.Entry{Marker: ui.MarkerEnabled, Name: name})
		problems = append(problems, target.problems...)
	}
	fmt.Print(ui.Render(preview))

	if hardBlocked(problems) {
		return fmt.Errorf("cannot enable all:\n%s", renderProblems(problems))
	}
	if !flags.Force {
		if !ui.Interactive() {
			return fmt.Errorf("cannot enable all non-interactively:\n%s\nrerun with --force to proceed", renderProblems(problems))
		}
		choice, err := ui.Prompt(
			fmt.Sprintf("enable %d namespace(s)?", len(targets)),
			[]string{"y", "N"},
		)
		if err != nil {
			return err
		}
		if !strings.EqualFold(choice, "y") && !strings.EqualFold(choice, "yes") {
			return nil
		}
	}

	for _, target := range targets {
		if err := engine.Enable(target.key, target.repoDir, target.nsDir, target.name, target.entries, s, target.problems); err != nil {
			return err
		}
	}
	return nil
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
