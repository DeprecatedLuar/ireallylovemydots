package commands

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/grammar"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/namespace"
	"github.com/DeprecatedLuar/dotz/internal/paths"
	"github.com/DeprecatedLuar/dotz/internal/repo"
	"github.com/DeprecatedLuar/dotz/internal/state"
	"github.com/DeprecatedLuar/dotz/internal/ui"
)

// HandleNamespace implements the namespace subtree: bare listing, the
// noun-level verbs that operate on a namespace by name in an argument
// (add, rm, mv, edit, enable, disable — the collection-level operand flip
// from concept.md "Aliases"), and per-namespace verbs reached by naming the
// namespace first (add, rm, list, edit, enable, disable, profiles). `add`
// does not flip: its collection-level meaning (create) and member-level
// meaning (track) are distinguished only by position.
func HandleNamespace(args []string, flags shared.Flags) error {
	if len(args) == 0 {
		return renderNamespaceList()
	}

	if grammar.IsNamespaceVerb(args[0]) {
		return handleNamespaceNounVerb(grammar.Canonical(args[0]), args[1:], flags)
	}

	name := args[0]
	rest := args[1:]
	if len(rest) == 0 {
		return renderNamespaceEntries(name, flags)
	}

	if grammar.CanonicalNoun(rest[0]) == "profiles" {
		return handleProfiles(name, rest[1:], flags)
	}

	if grammar.IsNamespaceVerb(rest[0]) {
		return handleNamespaceVerb(name, grammar.Canonical(rest[0]), rest[1:], flags)
	}

	// Neither a namespace verb nor the profiles noun: the token can only be a
	// profile name (concept.md "Profile level" shorthand, `dots <ns> <profile>`).
	// handleProfiles already treats args[0] as a profile name, so the fallback
	// is a plain handoff; a token that names no profile surfaces from there as
	// an unknown name rather than an unknown token.
	if err := refuseIfIgnored(name, flags); err != nil {
		return err
	}
	return handleProfiles(name, rest, flags)
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
		if len(args) == 0 {
			return fmt.Errorf("usage: namespace rm <name>...")
		}
		return rmNamespaces(args, flags)
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
	case "edit":
		if len(args) != 1 {
			return fmt.Errorf("usage: namespace edit <name>")
		}
		return editNamespace(args[0], flags)
	case "enable":
		return enableNamespaces(args, flags)
	case "disable":
		if len(args) != 1 {
			return fmt.Errorf("usage: namespace disable <name>")
		}
		return disableNamespace(args[0], flags)
	case "install":
		if len(args) == 0 {
			return fmt.Errorf("usage: namespace install <name>...")
		}
		return installNamespaces(args, flags)
	case "uninstall":
		if len(args) == 0 {
			return fmt.Errorf("usage: namespace uninstall <name>...")
		}
		return uninstallNamespaces(args, flags)
	case "ignore":
		if len(args) == 0 {
			return renderIgnoredNamespaces()
		}
		if len(args) != 1 {
			return fmt.Errorf("usage: namespace ignore [<name>]")
		}
		return ignoreNamespace(args[0], flags)
	case "unignore":
		if len(args) != 1 {
			return fmt.Errorf("usage: namespace unignore <name>")
		}
		return unignoreNamespace(args[0], flags)
	default:
		return fmt.Errorf("namespace %s: not valid without a namespace name", verb)
	}
}

// namespaceVerbsIgnoredMayUse are the only verbs an ignored namespace still
// answers to by name — concept.md "Namespace": an explicit opt-out must
// never look like "not found", so every other verb is refused, naming the
// flag, instead of acting on a namespace that declared itself out of scope.
var namespaceVerbsIgnoredMayUse = map[string]bool{"ignore": true, "unignore": true, "edit": true, "list": true, "mv": true}

func handleNamespaceVerb(name, verb string, args []string, flags shared.Flags) error {
	if !namespaceVerbsIgnoredMayUse[verb] {
		if err := refuseIfIgnored(name, flags); err != nil {
			return err
		}
	}
	switch verb {
	case "add":
		return trackPaths(name, args, flags)
	case "rm":
		if len(args) != 1 {
			return fmt.Errorf("usage: namespace %s rm <path>", name)
		}
		return rmEntry(name, args[0], flags)
	case "list":
		return renderNamespaceEntries(name, flags)
	case "edit":
		return editNamespace(name, flags)
	case "enable":
		return enableNamespace(name, flags)
	case "disable":
		return disableNamespace(name, flags)
	case "install":
		return installNamespaces([]string{name}, flags)
	case "uninstall":
		return uninstallNamespaces([]string{name}, flags)
	case "ignore":
		return ignoreNamespace(name, flags)
	case "unignore":
		return unignoreNamespace(name, flags)
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

// refuseIfIgnored is the one gate every namespace verb but ignore/unignore/
// edit/list/mv passes through: a namespace whose manifest carries
// `ignore = true` refuses by name, naming the flag, rather than acting on
// it or reporting "not found".
func refuseIfIgnored(name string, flags shared.Flags) error {
	loc, err := resolveNamespace(name, flags)
	if err != nil {
		return err
	}
	m, err := manifest.Read(loc.Dir)
	if err != nil {
		return err
	}
	if m.Ignore {
		return fmt.Errorf("namespace %q is ignored (ignore = true in its .dots)", name)
	}
	return nil
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
	choice, err := ui.Prompt("", "Multiple repositories registered. Choose one to hold the new namespace:", names)
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

// editNamespace implements `namespace <ns> edit`: open $EDITOR on a buffer
// seeded with the manifest plus an empty-destination entry for every
// untracked payload (concept.md "Manual edits"), and persist only what the
// editor actually leaves behind — quitting without saving must leave the
// real manifest byte-identical.
//
// The edit is guarded on the way back in, following visudo rather than a
// plain $EDITOR call (concept.md "Manual edits"): the result must parse
// before it replaces anything, and a parse error holds the terminal to ask
// whether to reopen the buffer at the error or discard the edit — the
// original file is untouched until answered. Not being able to prompt is a
// hard error, and the edit is discarded either way.
func editNamespace(name string, flags shared.Flags) error {
	loc, err := resolveNamespace(name, flags)
	if err != nil {
		return err
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		return fmt.Errorf("$EDITOR is not set")
	}

	original, err := prepareEditBuffer(loc.Dir)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp("", "dots-namespace-edit-*.toml")
	if err != nil {
		return fmt.Errorf("create edit buffer: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(original); err != nil {
		tmp.Close()
		return fmt.Errorf("write edit buffer: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close edit buffer: %w", err)
	}

	for {
		cmd := exec.Command(editor, tmpPath)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("run %s: %w", editor, err)
		}

		edited, err := os.ReadFile(tmpPath)
		if err != nil {
			return fmt.Errorf("read edit buffer: %w", err)
		}

		if bytes.Equal(original, edited) {
			return nil
		}

		if _, decodeErr := manifest.Decode(edited); decodeErr != nil {
			if !ui.Interactive() {
				return fmt.Errorf("%s does not parse: %w (edit discarded)", manifest.Path(loc.Dir), decodeErr)
			}
			choice, promptErr := ui.Prompt(
				"",
				fmt.Sprintf("%s does not parse: %v\n  [r] reopen at the error\n  [d] discard the edit", manifest.Path(loc.Dir), decodeErr),
				[]string{"r", "d"},
			)
			if promptErr != nil {
				return promptErr
			}
			if strings.EqualFold(strings.TrimSpace(choice), "r") {
				continue
			}
			return nil
		}

		return applyEditedBuffer(loc.Dir, original, edited)
	}
}

// prepareEditBuffer returns the bytes to seed namespace <ns> edit's buffer
// with: every entry already in the manifest, plus an empty-destination
// entry for every payload materialized in the namespace but absent from it.
// Untracked payloads come from namespace.Inspect — the same classification
// self-heal and listing use — rather than a second directory walk here, so
// a dotfile like ".profiled" is never silently excluded from the buffer
// just because its name happens to start with a dot; only the two names
// Inspect itself treats as plumbing (the manifest and its .gitignore) are
// skipped.
func prepareEditBuffer(namespaceDir string) ([]byte, error) {
	m, err := manifest.Read(namespaceDir)
	if err != nil {
		return nil, err
	}

	report, err := namespace.Inspect(namespaceDir, m.Entries)
	if err != nil {
		return nil, err
	}

	augmented := append([]manifest.Entry{}, m.Entries...)
	for _, n := range report.Untracked {
		augmented = append(augmented, manifest.Entry{Name: n, Dest: ""})
	}

	return manifest.Encode(manifest.Manifest{Ignore: m.Ignore, Entries: augmented})
}

// applyEditedBuffer persists the edited buffer as the namespace's manifest,
// but only if the editor actually changed it. Byte-identical means the
// editor quit without saving (or saved with no change), and the real
// manifest is left untouched either way.
func applyEditedBuffer(namespaceDir string, original, edited []byte) error {
	if bytes.Equal(original, edited) {
		return nil
	}
	m, err := manifest.Decode(edited)
	if err != nil {
		return fmt.Errorf("parse edited manifest: %w", err)
	}
	return manifest.Write(namespaceDir, m)
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

// ignoreNamespace implements `namespace ignore <name>`: writes
// `ignore = true` to the namespace's manifest, per concept.md "Namespace".
// Refused while the namespace is enabled — disabling first means self-heal
// is never asked to choose between abandoning live symlinks and honoring
// the flag. A namespace with no manifest yet gets one holding just the
// flag, which is what stops self-heal from regenerating one on its own.
func ignoreNamespace(name string, flags shared.Flags) error {
	loc, err := resolveNamespace(name, flags)
	if err != nil {
		return err
	}
	s, err := state.Read()
	if err != nil {
		return err
	}
	if s.Entries[state.Key{Repo: loc.Repo.Name, Namespace: name}].Enabled {
		return fmt.Errorf("namespace %q is enabled; disable it before ignoring it", name)
	}
	m, err := manifest.Read(loc.Dir)
	if err != nil {
		return err
	}
	if m.Ignore {
		return fmt.Errorf("namespace %q is already ignored", name)
	}
	m.Ignore = true
	return manifest.Write(loc.Dir, m)
}

// unignoreNamespace implements `namespace unignore <name>`: clears
// `ignore` from the namespace's manifest.
func unignoreNamespace(name string, flags shared.Flags) error {
	loc, err := resolveNamespace(name, flags)
	if err != nil {
		return err
	}
	m, err := manifest.Read(loc.Dir)
	if err != nil {
		return err
	}
	if !m.Ignore {
		return fmt.Errorf("namespace %q is not ignored", name)
	}
	m.Ignore = false
	return manifest.Write(loc.Dir, m)
}

// renderIgnoredNamespaces implements bare `namespace ignore`: lists every
// namespace currently ignored across every registered repository, since an
// explicit opt-out must stay discoverable rather than requiring the user to
// remember which folders they set it on.
func renderIgnoredNamespaces() error {
	reg, err := manifest.ReadRegistry()
	if err != nil {
		return err
	}
	dataDir, err := paths.Data()
	if err != nil {
		return err
	}
	var rows []ui.Entry
	for _, r := range reg.Repos {
		repoDir := filepath.Join(dataDir, r.Name)
		local, err := namespace.LocalNames(repoDir)
		if err != nil {
			return err
		}
		for _, n := range local {
			m, err := manifest.Read(filepath.Join(repoDir, n))
			if err != nil {
				return err
			}
			if m.Ignore {
				rows = append(rows, ui.Entry{Marker: ui.MarkerMaterialized, Name: n})
			}
		}
	}
	renderListing(rows)
	return nil
}

// renderNamespaceList lists every namespace across every registered
// repository, via the shared listing API in listing.go.
func renderNamespaceList() error {
	reg, err := manifest.ReadRegistry()
	if err != nil {
		return err
	}
	if len(reg.Repos) == 0 {
		printEmptyRegistryHint()
		return nil
	}
	rows, err := namespaceListing(reg.Repos, listOptions{})
	if err != nil {
		return err
	}
	renderListing(rows)
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
	s, err := state.Read()
	if err != nil {
		return err
	}
	stateEntry := s.Entries[state.Key{Repo: loc.Repo.Name, Namespace: name}]

	rows, suggestion, err := entryListing(loc.Dir, m.Entries, stateEntry.Enabled, stateEntry.ActiveProfile)
	if err != nil {
		return err
	}
	renderListing(rows)
	if suggestion != "" {
		fmt.Fprintln(os.Stderr, ui.Tip(suggestion))
	}
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
