package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/engine"
	"github.com/DeprecatedLuar/dotz/internal/git"
	"github.com/DeprecatedLuar/dotz/internal/grammar"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/paths"
	"github.com/DeprecatedLuar/dotz/internal/repo"
	"github.com/DeprecatedLuar/dotz/internal/state"
	"github.com/DeprecatedLuar/dotz/internal/ui"
)

// HandleRepo implements the repo subtree: bare listing, the noun-level
// verbs that operate on a repository by name in an argument (add, rm), and
// the per-repository verb reached by naming the repository first (list).
func HandleRepo(args []string, flags shared.Flags) error {
	if len(args) == 0 {
		return renderRepoList()
	}

	if grammar.IsRepoVerb(args[0]) {
		return handleRepoNounVerb(grammar.Canonical(args[0]), args[1:], flags)
	}

	name := args[0]
	rest := args[1:]
	if len(rest) == 0 {
		return fmt.Errorf("usage: repo %s list", name)
	}
	switch grammar.Canonical(rest[0]) {
	case "list":
		return renderRepoNamespaces(name)
	case "mv":
		if len(rest[1:]) != 1 {
			return fmt.Errorf("usage: repo %s mv <newname>", name)
		}
		if grammar.IsReserved(rest[1]) {
			return fmt.Errorf("%q is a reserved word and cannot be used as a repository name", rest[1])
		}
		return renameRepo(name, rest[1], flags)
	default:
		return fmt.Errorf("unknown verb %q for repo %q", rest[0], name)
	}
}

func handleRepoNounVerb(verb string, args []string, flags shared.Flags) error {
	switch verb {
	case "add":
		if len(args) != 1 {
			return fmt.Errorf("usage: repo add <url>")
		}
		return addRepo(args[0], flags)
	case "rm":
		if len(args) != 1 {
			return fmt.Errorf("usage: repo rm <repo>")
		}
		return rmRepo(args[0], flags)
	case "mv":
		if len(args) != 2 {
			return fmt.Errorf("usage: repo mv <repo> <newname>")
		}
		if grammar.IsReserved(args[1]) {
			return fmt.Errorf("%q is a reserved word and cannot be used as a repository name", args[1])
		}
		return renameRepo(args[0], args[1], flags)
	case "init":
		if len(args) > 1 {
			return fmt.Errorf("usage: repo init [path]")
		}
		var path string
		if len(args) == 1 {
			path = args[0]
		}
		return initRepo(path, flags)
	case "adopt":
		if len(args) != 1 {
			return fmt.Errorf("usage: repo adopt <name>")
		}
		return adoptRepo(args[0])
	case "list":
		return renderRepoList()
	default:
		return fmt.Errorf("repo %s: not valid without a repository name", verb)
	}
}

// addRepo implements `repo add <url>`: derive the local name and owner from
// the URL, resolve any collision with a reserved word or an already
// registered name, clone blobless with no checkout, check compatibility,
// then register — per concept.md "Compatibility", the order is clone,
// check, register. An incompatible result removes the clone and writes
// nothing to the registry, unless --bootstrap converts it first.
func addRepo(url string, flags shared.Flags) error {
	reg, err := manifest.ReadRegistry()
	if err != nil {
		return err
	}

	derivedName, owner := repo.DeriveNameOwner(url)
	name, err := resolveNewRepoName(reg, derivedName)
	if err != nil {
		return err
	}

	dataDir, err := paths.Data()
	if err != nil {
		return err
	}
	dest, resolvedURL, err := repo.Clone(dataDir, url, name)
	if err != nil {
		return err
	}

	entries, err := repo.RootEntries(dest)
	if err != nil {
		os.RemoveAll(dest)
		return err
	}
	state := repo.Inspect(entries)
	if state == repo.StateIncompatible {
		if !flags.Bootstrap {
			os.RemoveAll(dest)
			return fmt.Errorf("%s is not a dots repository: no top-level folder holds a .dots manifest; use --bootstrap to convert it", url)
		}

		converted, newState, err := bootstrapAdd(dest, url, entries)
		if err != nil {
			os.RemoveAll(dest)
			return err
		}
		if !converted {
			// Declined: the blobless clone made purely to inspect it is
			// discarded, leaving no trace, per concept.md "Bootstrap".
			os.RemoveAll(dest)
			return nil
		}
		state = newState
	}

	reg.Repos = append(reg.Repos, manifest.Repo{Name: name, Owner: owner, URL: resolvedURL})
	if err := manifest.WriteRegistry(reg); err != nil {
		os.RemoveAll(dest)
		return err
	}

	if state == repo.StateNamespaces {
		fmt.Print(ui.Render([]ui.Entry{{Marker: ui.MarkerMaterialized, Name: name}}))
	}
	return nil
}

// adoptRepo implements `repo adopt <name>`: the fix self-heal's unregistered-
// clone warning names, per concept.md "The data directory can drift from
// the registry too" — a directory already sitting in the data directory,
// left behind by a registry that lost its entry, gets registered without
// being touched on disk. Unlike addRepo, nothing is cloned and nothing is
// removed on failure; adopt only ever reads what is already there.
func adoptRepo(name string) error {
	reg, err := manifest.ReadRegistry()
	if err != nil {
		return err
	}
	if repoNameTaken(reg, name) {
		return fmt.Errorf("%q is already registered", name)
	}

	dataDir, err := paths.Data()
	if err != nil {
		return err
	}
	dest := filepath.Join(dataDir, name)
	if info, statErr := os.Stat(dest); statErr != nil || !info.IsDir() {
		return fmt.Errorf("%s is not a directory in the data directory, nothing to adopt", dest)
	}

	entries, err := repo.DiskEntries(dest)
	if err != nil {
		return err
	}
	if repo.Inspect(entries) != repo.StateNamespaces {
		return fmt.Errorf("%s is not a dots repository: no top-level folder holds a .dots manifest", dest)
	}

	isRepo, err := repo.IsGitRepo(dest)
	if err != nil {
		return err
	}
	var owner, url string
	origin := manifest.OriginLocal
	if isRepo {
		url, err = git.RemoteURL(dest)
		if err != nil {
			return err
		}
		if url != "" {
			_, owner = repo.DeriveNameOwner(url)
			origin = manifest.OriginConfig
		}
	}

	reg.Repos = append(reg.Repos, manifest.Repo{Name: name, Owner: owner, URL: url, Origin: origin})
	if err := manifest.WriteRegistry(reg); err != nil {
		return err
	}

	fmt.Print(ui.Render([]ui.Entry{{Marker: ui.MarkerMaterialized, Name: name}}))
	return nil
}

// bootstrapAdd runs `repo add --bootstrap`'s conversion at an already-cloned,
// blobless destination: plan, preview, prompt, and only on confirmation
// check out every blob and apply the conversion. Per concept.md "Bootstrap",
// every step up to the prompt is name-level, so declining costs nothing
// beyond the blobless clone repo add performs anyway. converted is false
// only when the user declined; a plan that converts to anything other than
// StateNamespaces is an error, since a conversion producing nothing usable
// must never register.
func bootstrapAdd(dest, url string, entries []repo.RootEntry) (converted bool, state repo.State, err error) {
	plan, proceed, err := planBootstrap(dest, entries)
	if err != nil {
		return false, repo.StateIncompatible, err
	}
	if !proceed {
		return false, repo.StateIncompatible, nil
	}

	if err := repo.CheckoutAll(dest); err != nil {
		return false, repo.StateIncompatible, err
	}
	if err := repo.Apply(dest, plan); err != nil {
		return false, repo.StateIncompatible, err
	}

	// Apply never commits, so the post-conversion state must be read
	// straight off disk rather than via RootEntries, which reads git's HEAD
	// tree and would still see the pre-conversion layout.
	postEntries, err := repo.DiskEntries(dest)
	if err != nil {
		return false, repo.StateIncompatible, err
	}
	state = repo.Inspect(postEntries)
	if state != repo.StateNamespaces {
		return false, repo.StateIncompatible, fmt.Errorf("--bootstrap conversion of %s produced no usable namespaces", url)
	}

	// CheckoutAll materialized the whole tree, undoing Clone's empty sparse
	// cone; the newly created namespace folders are exactly what belongs in
	// the cone now that they exist, per concept.md "Sparse checkout":
	// everything present is installed by definition. ReconcileCone reads
	// the cone from disk itself rather than trusting postEntries' HasDots
	// filter, which is what left krita-config's cone empty in the field —
	// a directory without a manifest still needs to be in the cone for git
	// to stage around it.
	if _, _, err := repo.ReconcileCone(dest); err != nil {
		return false, repo.StateIncompatible, err
	}
	return true, state, nil
}

// planBootstrap builds and previews a --bootstrap conversion of entries
// (already gathered from repoPath by the caller's compatibility check — via
// RootEntries for a git tree, DiskEntries for a plain folder not yet a
// repository) and prompts y/N, defaulting to no. An empty plan is a hard
// error, since --bootstrap has nothing to offer a repository with no
// convertible top-level entries. Declining is reported through the bool
// return, not an error. Not being able to prompt (no terminal) is itself a
// hard error, per concept.md "Bootstrap": "Not being able to prompt is a
// hard error, never a silent conversion."
func planBootstrap(repoPath string, entries []repo.RootEntry) ([]repo.PlannedNamespace, bool, error) {
	plan, err := repo.PlanEntries(entries)
	if err != nil {
		return nil, false, err
	}
	if len(plan) == 0 {
		return nil, false, fmt.Errorf("%s has no top-level entries for --bootstrap to convert", repoPath)
	}

	fmt.Print(ui.RenderAligned(bootstrapPreviewEntries(plan)))

	gitignoreChanges, err := repo.PlanGitignore(repoPath, plan)
	if err != nil {
		return nil, false, err
	}
	if preview := renderGitignorePreview(gitignoreChanges); preview != "" {
		fmt.Print("\n" + preview)
	}

	choice, err := ui.Prompt(
		"",
		fmt.Sprintf("--bootstrap will create %d namespace(s) as shown above. Proceed?", len(plan)),
		[]string{"y", "N"},
	)
	if err != nil {
		return nil, false, err
	}
	return plan, strings.EqualFold(choice, "y") || strings.EqualFold(choice, "yes"), nil
}

// renderGitignorePreview shows all three outcomes of the root .gitignore
// rewrite (concept.md "Bootstrap rewrites the root .gitignore") before the
// bootstrap confirmation prompt — the only place the conversion is agreed
// to. Blank lines and comments are real GitignoreUnchanged entries but are
// not worth showing; a repository with no .gitignore, or one holding only
// such lines, previews nothing.
func renderGitignorePreview(changes []repo.GitignoreChange) string {
	var b strings.Builder
	shown := 0
	for _, c := range changes {
		switch c.Outcome {
		case repo.GitignoreRewritten:
			fmt.Fprintf(&b, "  rewritten   %s -> %s\n", c.Original, c.Rewritten)
			shown++
		case repo.GitignoreUnmapped:
			fmt.Fprintf(&b, "  unmapped    %s      no such entry, left as-is\n", c.Original)
			shown++
		case repo.GitignoreUnchanged:
			trimmed := strings.TrimSpace(c.Original)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			fmt.Fprintf(&b, "  unchanged   %s\n", c.Original)
			shown++
		}
	}
	if shown == 0 {
		return ""
	}
	return ".gitignore\n" + b.String()
}

// bootstrapPreviewEntries renders the proposed conversion as aligned pairs,
// per concept.md "Bootstrap": "The preview is a proposed mapping, not a
// state listing", so it carries no listing marker.
func bootstrapPreviewEntries(plan []repo.PlannedNamespace) []ui.Pair {
	pairs := make([]ui.Pair, 0, len(plan))
	for _, p := range plan {
		pairs = append(pairs, ui.Pair{Name: p.Namespace, Value: manifest.ContractHome(p.Dest)})
	}
	return pairs
}

// initRepo implements `repo init [path]`: take a local folder and register
// it, with no remote. Per concept.md "Initializing a local folder", the
// order is check, take, register — inspection runs at the source path
// before anything is written, so a refused command leaves the folder
// exactly as it was found: not moved, and with no .git created in it.
func initRepo(pathArg string, flags shared.Flags) error {
	srcPath, err := resolveInitPath(pathArg)
	if err != nil {
		return err
	}

	inside, err := paths.InsideDataDir(srcPath)
	if err != nil {
		return err
	}
	if inside {
		return fmt.Errorf("%s is already inside the data directory", srcPath)
	}

	isRepo, err := repo.IsGitRepo(srcPath)
	if err != nil {
		return err
	}
	var entries []repo.RootEntry
	if isRepo {
		entries, err = repo.RootEntries(srcPath)
	} else {
		entries, err = repo.DiskEntries(srcPath)
	}
	if err != nil {
		return err
	}

	state := repo.Inspect(entries)
	var plan []repo.PlannedNamespace
	if state == repo.StateIncompatible {
		if !flags.Bootstrap {
			return fmt.Errorf("%s is not a dots repository: no top-level folder holds a .dots manifest; use --bootstrap to convert it", srcPath)
		}

		p, proceed, err := planBootstrap(srcPath, entries)
		if err != nil {
			return err
		}
		if !proceed {
			// Declining leaves the folder at its original path, per
			// concept.md "Bootstrap".
			return nil
		}
		plan = p
	}

	reg, err := manifest.ReadRegistry()
	if err != nil {
		return err
	}
	name, err := resolveNewRepoName(reg, filepath.Base(srcPath))
	if err != nil {
		return err
	}

	if err := repo.EnsureGit(srcPath); err != nil {
		return err
	}

	dataDir, err := paths.Data()
	if err != nil {
		return err
	}
	dest, err := repo.Take(dataDir, srcPath, name)
	if err != nil {
		return err
	}

	if plan != nil {
		if err := repo.Apply(dest, plan); err != nil {
			return err
		}
		// Nothing Apply writes is committed, so the post-conversion state
		// must be read straight off disk rather than via RootEntries, which
		// reads git's HEAD tree.
		postEntries, err := repo.DiskEntries(dest)
		if err != nil {
			return err
		}
		state = repo.Inspect(postEntries)
		if state != repo.StateNamespaces {
			return fmt.Errorf("--bootstrap conversion of %s produced no usable namespaces", srcPath)
		}
	}

	// repo init takes a folder whose content is already on disk in full, so
	// its cone is set to every top-level directory present — everything
	// present is installed by definition, per concept.md "Sparse checkout".
	if state == repo.StateNamespaces {
		if _, _, err := repo.ReconcileCone(dest); err != nil {
			return err
		}
	}

	// repo init has no remote, so this entry is never written to the shared
	// config registry — concept.md "Repository manifest": a local folder
	// path means nothing on another machine.
	reg.Repos = append(reg.Repos, manifest.Repo{Name: name, Origin: manifest.OriginLocal})
	if err := manifest.WriteRegistry(reg); err != nil {
		return err
	}

	if state == repo.StateNamespaces {
		fmt.Print(ui.Render([]ui.Entry{{Marker: ui.MarkerMaterialized, Name: name}}))
	}
	return nil
}

// resolveInitPath resolves repo init's optional path argument: the working
// directory with no argument, otherwise the argument itself, absolute —
// acting on the argument regardless of the process's current directory.
func resolveInitPath(pathArg string) (string, error) {
	if pathArg == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve working directory: %w", err)
		}
		return cwd, nil
	}
	abs, err := filepath.Abs(pathArg)
	if err != nil {
		return "", fmt.Errorf("resolve path %s: %w", pathArg, err)
	}
	return abs, nil
}

// resolveNewRepoName returns a name safe to register: not a reserved word,
// not already taken. It prompts for an alternative when interactive and
// errors naming the conflict otherwise.
func resolveNewRepoName(reg manifest.Registry, name string) (string, error) {
	for {
		var reason string
		switch {
		case grammar.IsReserved(name):
			reason = fmt.Sprintf("%q is a reserved word and cannot be used as a repository name.", name)
		case repoNameTaken(reg, name):
			reason = fmt.Sprintf("%q is already registered.", name)
		default:
			return name, nil
		}

		if !ui.Interactive() {
			return "", fmt.Errorf("%s rerun with an explicit distinct name (non-interactive session)", reason)
		}
		resp, err := ui.Prompt("", reason+" Choose a different local name:", nil)
		if err != nil {
			return "", err
		}
		resp = strings.TrimSpace(resp)
		if resp == "" {
			return "", fmt.Errorf("local name cannot be empty")
		}
		name = resp
	}
}

// renameRepo implements repo `mv`, reached from either spelling: resolve the
// registered repository, guard the new name against collisions, then rename
// its on-disk clone, its registry entry, and every state key recorded under
// its old name.
//
// Every symlink dots creates targets an absolute path built from the
// repository's own name (concept.md's data directory layout), so renaming it
// moves the target every one of its enabled namespaces' links point at, even
// though nothing about a manifest or a destination changed. relinkRenamedNamespaces
// repoints them as the last step of the rename, before this returns — left
// for the next self-heal pass, every link would dangle in the meantime and
// race whatever the destination's own program does when it finds nothing
// there.
func renameRepo(oldName, newName string, flags shared.Flags) error {
	reg, err := manifest.ReadRegistry()
	if err != nil {
		return err
	}
	r, err := repo.Resolve(reg.Repos, oldName)
	if err != nil {
		return err
	}
	if newName == r.Name {
		return nil
	}
	for _, other := range reg.Repos {
		if other.Name != r.Name && strings.EqualFold(other.Name, newName) {
			return fmt.Errorf("%q is already registered", newName)
		}
	}

	dataDir, err := paths.Data()
	if err != nil {
		return err
	}
	if err := repo.Rename(dataDir, r.Name, newName); err != nil {
		return err
	}

	for i := range reg.Repos {
		if reg.Repos[i].Name == r.Name {
			reg.Repos[i].Name = newName
			break
		}
	}
	if err := manifest.WriteRegistry(reg); err != nil {
		return err
	}

	if err := renameRepoState(r.Name, newName); err != nil {
		return err
	}

	failures, err := relinkRenamedNamespaces(filepath.Join(dataDir, newName), newName, nil)
	if err != nil {
		return err
	}
	reportRelinkFailures(failures)
	return nil
}

// renameRepoState rewrites every recorded state entry belonging to oldName
// onto newName, preserving each entry's Namespace and Entry value — a
// repository may hold many namespaces, so this is a full-map loop rather
// than the single-key move namespace.Rename does.
func renameRepoState(oldName, newName string) error {
	s, err := state.Read()
	if err != nil {
		return err
	}
	for k, e := range s.Entries {
		if k.Repo != oldName {
			continue
		}
		delete(s.Entries, k)
		s.Entries[state.Key{Repo: newName, Namespace: k.Namespace}] = e
	}
	return state.Write(s)
}

// relinkRenamedNamespaces repoints the links of every enabled namespace
// under repoName whose target moved because repoDir itself just moved on
// disk — a repository rename (repoDir is the whole repository's new
// location, names nil) or a single namespace rename within it (repoDir is
// unchanged, names narrows the repair to the one namespace that moved). It
// never rolls back: every reachable namespace still gets whatever repair
// engine.Relink can do, and the destinations it couldn't relink are returned
// for the caller to report rather than abort on — the rename already
// happened and isn't undone by one stubborn destination.
func relinkRenamedNamespaces(repoDir, repoName string, names map[string]bool) ([]engine.LinkFailure, error) {
	s, err := state.Read()
	if err != nil {
		return nil, err
	}

	var failures []engine.LinkFailure
	changed := false
	for key, entry := range s.Entries {
		if key.Repo != repoName || !entry.Enabled {
			continue
		}
		if names != nil && !names[key.Namespace] {
			continue
		}

		namespaceDir := filepath.Join(repoDir, key.Namespace)
		if _, statErr := os.Stat(namespaceDir); statErr != nil {
			// Not materialized on this machine — nothing on disk to relink.
			continue
		}
		exists, existsErr := manifest.Exists(namespaceDir)
		if existsErr != nil {
			return nil, existsErr
		}
		if !exists {
			// A missing manifest here is self-heal's manifest-recovery job,
			// not a rename's — it never reads or writes a manifest, and
			// recovering one from a guess is not this operation's call to
			// make.
			continue
		}
		m, readErr := manifest.Read(namespaceDir)
		if readErr != nil {
			return nil, readErr
		}
		if m.Ignore {
			continue
		}

		linked, nsFailures, relinkErr := engine.Relink(namespaceDir, m.Entries, entry.ActiveProfile)
		if relinkErr != nil {
			return nil, relinkErr
		}
		entry.LinkedDests = linked
		s.Entries[key] = entry
		changed = true
		failures = append(failures, nsFailures...)
	}

	if changed {
		if err := state.Write(s); err != nil {
			return nil, err
		}
	}
	return failures, nil
}

// reportRelinkFailures prints every destination relinkRenamedNamespaces
// could not bring into place — a real file or directory occupying it, the
// same "never destroyed, only reported" rule self-heal and enable follow —
// so a rename that leaves one destination unlinked doesn't do so silently.
func reportRelinkFailures(failures []engine.LinkFailure) {
	if len(failures) == 0 {
		return
	}
	for _, f := range failures {
		fmt.Fprintln(os.Stderr, ui.WarningTone(fmt.Sprintf("! %s%s%s", manifest.ContractHome(f.Dest), ui.DetailSep, f.Detail)))
	}
	fmt.Fprintln(os.Stderr, ui.Tip("run `dots enable <namespace>` to retry, add --force to trash the occupant"))
}

func repoNameTaken(reg manifest.Registry, name string) bool {
	for _, r := range reg.Repos {
		if strings.EqualFold(r.Name, name) {
			return true
		}
	}
	return false
}

// renderRepoList lists every registered repository, marking one whose clone
// is missing from the data directory "!" rather than silently deregistering
// it — concept.md "Repository manifest": absence is ambiguous, and a
// mis-set XDG_DATA_HOME must not be able to erase the registry.
func renderRepoList() error {
	reg, err := manifest.ReadRegistry()
	if err != nil {
		return err
	}
	if len(reg.Repos) == 0 {
		printEmptyRegistryHint()
		return nil
	}
	rows, err := repoListing(reg.Repos)
	if err != nil {
		return err
	}
	renderListing(rows)
	return nil
}

// renderRepoNamespaces lists one repository's namespaces via the shared
// listing API in listing.go, narrowed to this repository — so it carries
// the same "+"/"-"/"=" markers as the unscoped listing, not a hardcoded "="
// for everything.
func renderRepoNamespaces(name string) error {
	reg, err := manifest.ReadRegistry()
	if err != nil {
		return err
	}
	r, err := repo.Resolve(reg.Repos, name)
	if err != nil {
		return err
	}

	rows, err := namespaceListing(reg.Repos, listOptions{Repo: r.Name})
	if err != nil {
		return err
	}
	renderListing(rows)
	return nil
}

// RepoNames returns every registered repository's local name, used by the
// router to resolve a bare top-level token against known repositories.
func RepoNames() ([]string, error) {
	reg, err := manifest.ReadRegistry()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(reg.Repos))
	for _, r := range reg.Repos {
		names = append(names, r.Name)
	}
	return names, nil
}
