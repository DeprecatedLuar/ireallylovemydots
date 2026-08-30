package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/grammar"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/namespace"
	"github.com/DeprecatedLuar/dotz/internal/paths"
	"github.com/DeprecatedLuar/dotz/internal/repo"
	"github.com/DeprecatedLuar/dotz/internal/ui"
)

// HandleNamespace implements the namespace subtree: bare listing, the
// noun-level verbs that operate on a namespace by name in an argument
// (add, rm, mv), and per-namespace verbs reached by naming the namespace
// first (add, rm, list, edit, enable, disable, profiles).
//
// Enable, disable, and removal are built in later phases; here each such
// verb validates what phase 2 owns (reserved names, argument shape) and
// reports itself as not yet implemented.
func HandleNamespace(args []string, flags shared.Flags) error {
	if len(args) == 0 {
		return renderNamespaceList()
	}

	if grammar.IsVerb(args[0]) {
		return handleNamespaceNounVerb(grammar.Canonical(args[0]), args[1:], flags)
	}

	name := args[0]
	rest := args[1:]
	if len(rest) == 0 {
		return renderNamespaceEntries(name, flags)
	}

	if rest[0] == "profiles" {
		return handleProfiles(name, rest[1:], flags)
	}

	if !grammar.IsVerb(rest[0]) {
		return fmt.Errorf("unknown token %q after namespace %q", rest[0], name)
	}
	return handleNamespaceVerb(name, grammar.Canonical(rest[0]), rest[1:], flags)
}

func handleNamespaceNounVerb(verb string, args []string, flags shared.Flags) error {
	switch verb {
	case "add":
		if len(args) != 1 {
			return fmt.Errorf("usage: namespace add <name>")
		}
		name := args[0]
		if grammar.IsReserved(name) {
			return fmt.Errorf("%q is a reserved name and cannot be used for a namespace", name)
		}
		return createNamespace(name, flags)
	case "rm":
		if len(args) != 1 {
			return fmt.Errorf("usage: namespace rm <name>")
		}
		return errNotImplemented("namespace rm")
	case "mv":
		if len(args) != 2 {
			return fmt.Errorf("usage: namespace mv <name> <newname>")
		}
		if grammar.IsReserved(args[1]) {
			return fmt.Errorf("%q is a reserved name and cannot be used for a namespace", args[1])
		}
		return renameNamespace(args[0], args[1], flags)
	case "list":
		return renderNamespaceList()
	default:
		return fmt.Errorf("namespace %s: not valid without a namespace name", verb)
	}
}

func handleNamespaceVerb(name, verb string, args []string, flags shared.Flags) error {
	switch verb {
	case "add":
		return trackPaths(name, args, flags)
	case "rm":
		return errNotImplemented(fmt.Sprintf("namespace %s rm", name))
	case "list":
		return renderNamespaceEntries(name, flags)
	case "edit":
		return editNamespace(name, flags)
	case "enable":
		return errNotImplemented(fmt.Sprintf("namespace %s enable", name))
	case "disable":
		return errNotImplemented(fmt.Sprintf("namespace %s disable", name))
	case "mv":
		if len(args) != 1 {
			return fmt.Errorf("usage: namespace %s mv <newname>", name)
		}
		if grammar.IsReserved(args[0]) {
			return fmt.Errorf("%q is a reserved name and cannot be used for a namespace", args[0])
		}
		return renameNamespace(name, args[0], flags)
	default:
		return fmt.Errorf("unknown verb %q for namespace %q", verb, name)
	}
}

// createNamespace implements `namespace add <name>`: resolves which
// registered repository holds the new namespace (the sole repository, a
// prompted choice, or --repo) and creates an empty namespace folder there.
func createNamespace(name string, flags shared.Flags) error {
	reg, err := manifest.ReadRegistry()
	if err != nil {
		return err
	}
	r, err := resolveTargetRepo(reg, flags)
	if err != nil {
		return err
	}
	dataDir, err := paths.Data()
	if err != nil {
		return err
	}

	dir, err := namespace.Create(filepath.Join(dataDir, r.Name), name)
	if err != nil {
		return err
	}
	fmt.Print(ui.Render([]ui.Entry{{Marker: ui.MarkerMaterialized, Name: filepath.Base(dir)}}))
	return nil
}

// resolveTargetRepo picks the repository a new namespace belongs to, per
// concept.md "Namespace level": the sole registered repository, --repo when
// given, or a prompt (hard error non-interactively) when several exist.
func resolveTargetRepo(reg manifest.Registry, flags shared.Flags) (manifest.Repo, error) {
	if flags.Repo != "" {
		return repo.Resolve(reg.Repos, flags.Repo)
	}
	switch len(reg.Repos) {
	case 0:
		return manifest.Repo{}, fmt.Errorf("no repositories registered; run repo add first")
	case 1:
		return reg.Repos[0], nil
	}

	if !ui.Interactive() {
		return manifest.Repo{}, fmt.Errorf("multiple repositories registered; specify --repo")
	}
	names := make([]string, 0, len(reg.Repos))
	for _, r := range reg.Repos {
		names = append(names, r.Name)
	}
	choice, err := ui.Prompt("Multiple repositories registered. Choose one to hold the new namespace:", names)
	if err != nil {
		return manifest.Repo{}, err
	}
	return repo.Resolve(reg.Repos, choice)
}

// trackPaths implements `namespace <ns> add <path>...`.
func trackPaths(name string, args []string, flags shared.Flags) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: namespace %s add <path>...", name)
	}
	loc, err := resolveNamespace(name, flags)
	if err != nil {
		return err
	}
	for _, p := range args {
		if err := namespace.Add(loc.Dir, p); err != nil {
			return err
		}
	}
	return nil
}

// renameNamespace implements `mv`, reached from either spelling.
func renameNamespace(oldName, newName string, flags shared.Flags) error {
	loc, err := resolveNamespace(oldName, flags)
	if err != nil {
		return err
	}
	return namespace.Rename(filepath.Dir(loc.Dir), loc.Repo.Name, oldName, newName)
}

// editNamespace implements `namespace <ns> edit`: open $EDITOR on the
// namespace's manifest directly.
func editNamespace(name string, flags shared.Flags) error {
	loc, err := resolveNamespace(name, flags)
	if err != nil {
		return err
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		return fmt.Errorf("$EDITOR is not set")
	}
	cmd := exec.Command(editor, manifest.Path(loc.Dir))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run %s: %w", editor, err)
	}
	return nil
}

// resolveNamespace finds the locally materialized namespace called name,
// disambiguating across repositories via flags.Repo when needed.
func resolveNamespace(name string, flags shared.Flags) (namespace.Located, error) {
	reg, err := manifest.ReadRegistry()
	if err != nil {
		return namespace.Located{}, err
	}
	dataDir, err := paths.Data()
	if err != nil {
		return namespace.Located{}, err
	}
	return namespace.Resolve(dataDir, reg.Repos, name, flags.Repo)
}

// renderNamespaceList lists every namespace across every registered
// repository: materialized namespaces (created or tracked locally, whether
// committed yet or not) marked "-", and namespaces that exist in a
// repository's catalogue but are not materialized here, marked "=". Drift
// and conflict markers ("!", "?") are phase 7's job.
func renderNamespaceList() error {
	reg, err := manifest.ReadRegistry()
	if err != nil {
		return err
	}
	dataDir, err := paths.Data()
	if err != nil {
		return err
	}

	var entries []ui.Entry
	for _, r := range reg.Repos {
		repoDir := filepath.Join(dataDir, r.Name)

		local, err := namespace.LocalNames(repoDir)
		if err != nil {
			return err
		}
		localSet := make(map[string]bool, len(local))
		for _, n := range local {
			localSet[n] = true
			entries = append(entries, ui.Entry{Marker: ui.MarkerMaterialized, Name: n})
		}

		catalogue, err := repo.Namespaces(repoDir)
		if err != nil {
			return err
		}
		for _, n := range catalogue {
			if !localSet[n] {
				entries = append(entries, ui.Entry{Marker: ui.MarkerAbsent, Name: n})
			}
		}
	}
	fmt.Print(ui.Render(entries))
	return nil
}

func renderNamespaceEntries(name string, flags shared.Flags) error {
	loc, err := resolveNamespace(name, flags)
	if err != nil {
		return err
	}
	m, err := manifest.Read(loc.Dir)
	if err != nil {
		return err
	}
	entries := make([]ui.Entry, 0, len(m.Entries))
	for _, e := range m.Entries {
		entries = append(entries, ui.Entry{Marker: ui.MarkerMaterialized, Name: e.Name})
	}
	fmt.Print(ui.Render(entries))
	return nil
}

// NamespaceNames returns every namespace name materialized locally across
// every registered repository, used by the router to resolve a bare
// top-level token against known namespaces.
func NamespaceNames() ([]string, error) {
	reg, err := manifest.ReadRegistry()
	if err != nil {
		return nil, err
	}
	dataDir, err := paths.Data()
	if err != nil {
		return nil, err
	}

	var names []string
	for _, r := range reg.Repos {
		local, err := namespace.LocalNames(filepath.Join(dataDir, r.Name))
		if err != nil {
			return nil, err
		}
		names = append(names, local...)
	}
	return names, nil
}

func errNotImplemented(what string) error {
	return fmt.Errorf("%s: not yet implemented", what)
}
