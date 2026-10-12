package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DeprecatedLuar/ireallylovemydots/internal/commands/shared"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/engine"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/git"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/grammar"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/manifest"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/namespace"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/paths"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/repo"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/selfheal"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/state"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/ui"
)

const (
	registryFilePerm = 0644

	bootstrapPlanLabel     = "bootstrap plan"
	bootstrapRootNoDest    = "entries below"
	bootstrapPreviewIndent = "  "
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
	case "rn":
		if len(rest[1:]) != 1 {
			return fmt.Errorf("usage: repo %s rn <newname>", name)
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
	case "rn":
		if len(args) != 2 {
			return fmt.Errorf("usage: repo rn <repo> <newname>")
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
	case "edit":
		if len(args) != 0 {
			return fmt.Errorf("usage: repo edit")
		}
		return editRegistry()
	case "list":
		return renderRepoList()
	default:
		return fmt.Errorf("repo %s: not valid without a repository name", verb)
	}
}

// promoteLocalRepo moves an already-registered local entry whose clone has
// gained a remote into the shared repository manifest, per concept.md "The
// data directory can drift from the registry too". The clone is not
// touched. An entry already shared, or a clone with no remote, is refused.
func promoteLocalRepo(reg manifest.Registry, name string) error {
	for i, r := range reg.Repos {
		if !strings.EqualFold(r.Name, name) {
			continue
		}
		if r.Origin != manifest.OriginLocal {
			return fmt.Errorf("%q is already registered", r.Name)
		}
		dataDir, err := paths.Data()
		if err != nil {
			return err
		}
		url, err := git.RemoteURL(filepath.Join(dataDir, r.Name))
		if err != nil {
			return err
		}
		if url == "" {
			return fmt.Errorf("%q is registered locally and its clone has no remote; run `git remote add origin <url>` in it first", r.Name)
		}
		_, owner := repo.DeriveNameOwner(url)
		reg.Repos[i].URL = url
		reg.Repos[i].Owner = owner
		reg.Repos[i].Origin = manifest.OriginConfig
		if err := manifest.WriteRegistry(reg); err != nil {
			return err
		}
		fmt.Print(ui.Render([]ui.Entry{{Marker: ui.MarkerMaterialized, Name: r.Name}}))
		return nil
	}
	return fmt.Errorf("%q is not registered", name)
}

// editRegistry implements `repo edit`: the shared repository manifest in
// $EDITOR, through the same edit-buffer contract as namespace edit. Local
// entries are machine state and are not offered for editing.
func editRegistry() error {
	path, err := manifest.RegistryPath()
	if err != nil {
		return err
	}
	seed, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	_, err = editBuffer(seed, path,
		func(edited []byte) error {
			_, err := manifest.DecodeRegistry(edited)
			return err
		},
		func(_, edited []byte) error {
			if err := os.WriteFile(path, edited, registryFilePerm); err != nil {
				return fmt.Errorf("write %s: %w", path, err)
			}
			return nil
		},
	)
	return err
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
			return fmt.Errorf("%s is not a dots repository: no .dots manifest at its root or in any top-level folder; use --bootstrap to convert it", url)
		}

		converted, newState, err := bootstrapAdd(dest, url, name, entries)
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

	if err := registerRepo(reg, manifest.Repo{Name: name, Owner: owner, URL: resolvedURL}); err != nil {
		os.RemoveAll(dest)
		return err
	}

	probeAndRecordAccess(dest, name)

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
// removed on failure; adopt only ever reads what is already there. An
// already-registered local entry is promoted instead — see promoteLocalRepo.
func adoptRepo(name string) error {
	reg, err := manifest.ReadRegistry()
	if err != nil {
		return err
	}
	if repoNameTaken(reg, name) {
		return promoteLocalRepo(reg, name)
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
		return fmt.Errorf("%s is not a dots repository: no .dots manifest at its root or in any top-level folder", dest)
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

	if err := registerRepo(reg, manifest.Repo{Name: name, Owner: owner, URL: url, Origin: origin}); err != nil {
		return err
	}

	fmt.Print(ui.Render([]ui.Entry{{Marker: ui.MarkerMaterialized, Name: name}}))
	return nil
}

// bootstrapAdd runs `repo add --bootstrap`'s conversion at an already-cloned,
// blobless destination: plan, preview, prompt, and only on confirmation
// apply the conversion, checking out every blob first when the plan moves
// anything. Every step up to the prompt is name-level, so declining costs
// nothing beyond the blobless clone. converted is false only when the user
// declined; a conversion that does not yield StateNamespaces is an error.
func bootstrapAdd(dest, url, name string, entries []repo.RootEntry) (converted bool, state repo.State, err error) {
	plan, proceed, err := planBootstrap(dest, name, entries)
	if err != nil {
		return false, repo.StateIncompatible, err
	}
	if !proceed {
		return false, repo.StateIncompatible, nil
	}

	if len(plan.Moves(entries)) > 0 {
		if err := repo.CheckoutAll(dest); err != nil {
			return false, repo.StateIncompatible, err
		}
	}
	if err := repo.ApplyBootstrap(dest, plan, entries); err != nil {
		return false, repo.StateIncompatible, err
	}

	// ApplyBootstrap does not commit, so read the result from disk;
	// RootEntries reads HEAD and would see the old layout.
	postEntries, err := repo.DiskEntries(dest)
	if err != nil {
		return false, repo.StateIncompatible, err
	}
	state = repo.Inspect(postEntries)
	if state != repo.StateNamespaces {
		return false, repo.StateIncompatible, fmt.Errorf("--bootstrap conversion of %s produced no usable namespaces", url)
	}

	// Everything the conversion produced is installed, so the cone covers
	// every top-level directory present, including directories that have no
	// manifest of their own (root namespace content).
	if _, _, err := repo.ReconcileCone(dest); err != nil {
		return false, repo.StateIncompatible, err
	}
	return true, state, nil
}

// planBootstrap opens the editable bootstrap plan in $EDITOR, previews the
// result, and asks y/N, defaulting to no. Closing the editor unchanged
// accepts the plan as offered; discarding cancels. Not being able to prompt
// is a hard error, per concept.md "Bootstrap".
func planBootstrap(repoPath, repoName string, entries []repo.RootEntry) (repo.BootstrapPlan, bool, error) {
	if !ui.Interactive() {
		return repo.BootstrapPlan{}, false, fmt.Errorf("--bootstrap needs a terminal: its plan opens in $EDITOR")
	}
	plan, proceed, err := editBootstrapPlan(repoPath, repoName, entries)
	if err != nil || !proceed {
		return repo.BootstrapPlan{}, false, err
	}

	moves := plan.Moves(entries)
	fmt.Print(ui.RenderAligned(bootstrapPreviewEntries(plan, moves)))
	if len(moves) > 0 {
		gitignoreChanges, err := repo.PlanGitignore(repoPath, moves)
		if err != nil {
			return repo.BootstrapPlan{}, false, err
		}
		if preview := renderGitignorePreview(gitignoreChanges); preview != "" {
			fmt.Print("\n" + preview)
		}
	}

	choice, err := ui.Prompt("", "--bootstrap will apply the plan shown above. Proceed?", []string{"y", "N"})
	if err != nil {
		return repo.BootstrapPlan{}, false, err
	}
	return plan, ui.IsYes(choice), nil
}

// editBootstrapPlan runs the default plan through the $EDITOR loop and
// returns the plan the editor closed on. proceed is false only when an
// invalid edit was discarded.
func editBootstrapPlan(repoPath, repoName string, entries []repo.RootEntry) (plan repo.BootstrapPlan, proceed bool, err error) {
	plan, err = repo.DefaultBootstrapPlan(repoName, entries)
	if err != nil {
		return repo.BootstrapPlan{}, false, err
	}
	if len(plan.Root) == 0 {
		return repo.BootstrapPlan{}, false, fmt.Errorf("%s has no top-level entries for --bootstrap to convert", repoPath)
	}

	seed := repo.EncodeBootstrapPlan(plan)
	chosen := seed
	outcome, err := editBuffer(seed, bootstrapPlanLabel,
		func(edited []byte) error {
			_, err := repo.DecodeBootstrapPlan(edited, entries)
			return err
		},
		func(_, edited []byte) error {
			chosen = edited
			return nil
		})
	if err != nil {
		return repo.BootstrapPlan{}, false, err
	}
	if outcome == editDiscarded {
		return repo.BootstrapPlan{}, false, nil
	}
	plan, err = repo.DecodeBootstrapPlan(chosen, entries)
	if err != nil {
		return repo.BootstrapPlan{}, false, err
	}
	return plan, true, nil
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

// bootstrapPreviewEntries renders the plan as aligned pairs: the root
// namespace with its overrides indented beneath it, then each move. Per
// concept.md "Bootstrap" it is a proposed mapping, so it carries no
// listing marker.
func bootstrapPreviewEntries(plan repo.BootstrapPlan, moves []repo.PlannedNamespace) []ui.Pair {
	var pairs []ui.Pair
	if plan.Dest != "" || len(plan.Root) > 0 {
		value := plan.Dest
		if value == "" {
			value = bootstrapRootNoDest
		}
		pairs = append(pairs, ui.Pair{Name: plan.Name, Value: value})
		names := make([]string, 0, len(plan.Root))
		for n := range plan.Root {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			pairs = append(pairs, ui.Pair{Name: bootstrapPreviewIndent + n, Value: plan.Root[n]})
		}
	}
	for _, m := range moves {
		pairs = append(pairs, ui.Pair{Name: m.Namespace, Value: manifest.DisplayPath(m.Dest)})
	}
	return pairs
}

// initRepo implements `repo init [path]`: take a local folder and register
// it, with no remote. The order is check, take, register: inspection runs at
// the source path before anything is written, so a refused command leaves
// the folder untouched.
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
	var plan repo.BootstrapPlan
	bootstrapping := false
	if state == repo.StateIncompatible {
		if !flags.Bootstrap {
			return fmt.Errorf("%s is not a dots repository: no .dots manifest at its root or in any top-level folder; use --bootstrap to convert it", srcPath)
		}

		p, proceed, err := planBootstrap(srcPath, filepath.Base(srcPath), entries)
		if err != nil {
			return err
		}
		if !proceed {
			// Declining leaves the folder at its original path.
			return nil
		}
		plan = p
		bootstrapping = true
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

	if bootstrapping {
		if err := repo.ApplyBootstrap(dest, plan, entries); err != nil {
			return err
		}
		// ApplyBootstrap does not commit, so read the result from disk.
		postEntries, err := repo.DiskEntries(dest)
		if err != nil {
			return err
		}
		state = repo.Inspect(postEntries)
		if state != repo.StateNamespaces {
			return fmt.Errorf("--bootstrap conversion of %s produced no usable namespaces", srcPath)
		}
	}

	// The folder is fully on disk, so the cone covers every top-level
	// directory present.
	if state == repo.StateNamespaces {
		if _, _, err := repo.ReconcileCone(dest); err != nil {
			return err
		}
	}

	// A local folder path means nothing on another machine, so the entry
	// stays out of the shared config registry.
	if err := registerRepo(reg, manifest.Repo{Name: name, Origin: manifest.OriginLocal}); err != nil {
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

// renameRepo implements repo `rn`, reached from either spelling: resolve the
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
	if err := renameRepoAccess(r.Name, newName); err != nil {
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

// renameRepoAccess moves oldName's recorded push access, if any, onto
// newName — the same machine-local rename renameRepoState already gives
// state.Entries, so a renamed read-only repository doesn't silently read as
// pushable again under its new name.
func renameRepoAccess(oldName, newName string) error {
	access, err := state.ReadAccess()
	if err != nil {
		return err
	}
	if !access.IsReadOnly(oldName) {
		return nil
	}
	delete(access.ReadOnly, oldName)
	access.SetReadOnly(newName, true)
	return state.WriteAccess(access)
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

		namespaceDir, dirErr := repo.NamespaceDir(repoDir, key.Namespace)
		if dirErr != nil {
			return nil, dirErr
		}
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

		entries, linkErr := namespace.Linkable(namespaceDir, m)
		if linkErr != nil {
			return nil, linkErr
		}
		linked, nsFailures, relinkErr := engine.Relink(namespaceDir, entries, entry.ActiveProfile)
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
		fmt.Fprintln(os.Stderr, ui.WarningTone("! "+ui.BlockedSummary([]ui.Blocked{{Dest: f.Dest, Detail: f.Detail}}, "")))
	}
	fmt.Fprintln(os.Stderr, ui.Tip("run `dots enable <namespace>` to retry, add --force to trash the occupant"))
}

// probeAndRecordAccess checks whether this machine can push to the
// newly-cloned repository at dest and records the result under name in the
// machine-local access store, per concept.md "Repository manifest": push
// capability is per-machine, never shared. A probe failure is not fatal to
// `repo add` — it only means the repository is left unflagged, to be
// resolved the next time something tries to push.
func probeAndRecordAccess(dest, name string) {
	canPush, err := git.CanPush(dest)
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.WarningTone(fmt.Sprintf("! %s%scould not determine push access: %v", name, ui.DetailSep, err)))
		return
	}
	if canPush {
		return
	}

	access, err := state.ReadAccess()
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.WarningTone(fmt.Sprintf("! %s%scould not record read-only access: %v", name, ui.DetailSep, err)))
		return
	}
	access.SetReadOnly(name, true)
	if err := state.WriteAccess(access); err != nil {
		fmt.Fprintln(os.Stderr, ui.WarningTone(fmt.Sprintf("! %s%scould not record read-only access: %v", name, ui.DetailSep, err)))
	}
}

// registerRepo appends entry to reg.Repos and writes the registry back to
// disk. It never rolls back anything itself — that stays whatever it was at
// each call site, since only some of them have something to unwind (a clone
// already made) on failure.
func registerRepo(reg manifest.Registry, entry manifest.Repo) error {
	reg.Repos = append(reg.Repos, entry)
	return manifest.WriteRegistry(reg)
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

	rows, err := namespaceListing(reg.Repos, listOptions{Repo: r.Name}, selfheal.Findings{})
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
