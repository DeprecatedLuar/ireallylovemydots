package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/engine"
	"github.com/DeprecatedLuar/dotz/internal/grammar"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/profile"
	"github.com/DeprecatedLuar/dotz/internal/state"
	"github.com/DeprecatedLuar/dotz/internal/ui"
)

// handleProfiles implements the profiles subtree of one namespace: bare
// listing, the noun-level verbs that operate on a profile by name in an
// argument (add, rm, mv), main's three membership verbs, and per-profile
// verbs reached by naming the profile first (enable, disable, add, rm,
// list) — concept.md "Profile level".
func handleProfiles(namespace string, args []string, flags shared.Flags) error {
	if len(args) == 0 {
		return renderProfileList(namespace, flags)
	}

	if grammar.IsVerb(args[0]) {
		return handleProfilesNounVerb(namespace, grammar.Canonical(args[0]), args[1:], flags)
	}

	name := args[0]
	rest := args[1:]
	if len(rest) == 0 {
		if name == profile.Main {
			return returnToMain(namespace, flags)
		}
		return enableProfile(namespace, name, flags)
	}

	if name == profile.Main {
		return handleProfilesMain(namespace, grammar.Canonical(rest[0]), rest[1:], flags)
	}

	switch grammar.Canonical(rest[0]) {
	case "enable":
		return enableProfile(namespace, name, flags)
	case "disable":
		return disableProfile(namespace, name, flags)
	case "add":
		if len(rest) != 2 {
			return fmt.Errorf("usage: namespace %s profiles %s add <entry>", namespace, name)
		}
		return addOverride(namespace, name, rest[1], flags)
	case "rm":
		if len(rest) != 2 {
			return fmt.Errorf("usage: namespace %s profiles %s rm <entry>", namespace, name)
		}
		return dropOverride(namespace, name, rest[1], flags)
	case "list":
		return renderProfileEntries(namespace, name, flags)
	default:
		return fmt.Errorf("unknown verb %q for namespace %q profile %q", rest[0], namespace, name)
	}
}

func handleProfilesNounVerb(namespace, verb string, args []string, flags shared.Flags) error {
	switch verb {
	case "add":
		if len(args) != 1 {
			return fmt.Errorf("usage: namespace %s profiles add <profile>", namespace)
		}
		name := args[0]
		if grammar.IsReservedProfile(name) {
			return fmt.Errorf("%q is a reserved name and cannot be used for a profile", name)
		}
		return addProfile(namespace, name, flags)
	case "rm":
		if len(args) != 1 {
			return fmt.Errorf("usage: namespace %s profiles rm <profile>", namespace)
		}
		return rmProfile(namespace, args[0], flags)
	case "mv":
		if len(args) != 2 {
			return fmt.Errorf("usage: namespace %s profiles mv <profile> <newname>", namespace)
		}
		if grammar.IsReservedProfile(args[1]) {
			return fmt.Errorf("%q is a reserved name and cannot be used for a profile", args[1])
		}
		return mvProfile(namespace, args[0], args[1], flags)
	case "list":
		return renderProfileList(namespace, flags)
	case "edit":
		return editProfiles(namespace, flags)
	default:
		return fmt.Errorf("namespace %s profiles %s: not valid without a profile name", namespace, verb)
	}
}

// handleProfilesMain routes main's three membership verbs. main is the
// namespace root, not a profile: it cannot be enabled, created, renamed or
// removed (concept.md "main").
func handleProfilesMain(namespace, verb string, args []string, flags shared.Flags) error {
	switch verb {
	case "list":
		return renderProfiledEntries(namespace, flags)
	case "add":
		if len(args) != 1 {
			return fmt.Errorf("usage: namespace %s profiles main add <entry>", namespace)
		}
		return declareEntry(namespace, args[0], flags)
	case "rm":
		if len(args) != 1 {
			return fmt.Errorf("usage: namespace %s profiles main rm <entry>", namespace)
		}
		return undeclareEntry(namespace, args[0], flags)
	default:
		return fmt.Errorf("main is the namespace root, not a profile; it takes only list, add and rm")
	}
}

// profileScope is everything every profile verb needs about one namespace:
// where it lives, what it tracks, what it offers, and which profile this
// machine currently has active.
type profileScope struct {
	name    string
	dir     string
	key     state.Key
	entries []manifest.Entry
	profile profile.Manifest
	state   state.State
	active  string
}

// scopeFor resolves one namespace and reads every manifest a profile verb
// works against, so no verb below repeats the four-step lookup. Every
// profile verb but `profiles edit` requires the profile manifest to already
// exist: its presence is what says the namespace has opted into a profile
// layer at all, and only `profiles edit` may create it (concept.md "The
// profile manifest > Creating it"). editProfiles builds its own scope
// instead of calling this, since it is the one path allowed to proceed
// without the file.
func scopeFor(namespaceName string, flags shared.Flags) (profileScope, error) {
	loc, err := resolveNamespace(namespaceName, flags)
	if err != nil {
		return profileScope{}, err
	}
	exists, err := profile.Exists(loc.Dir)
	if err != nil {
		return profileScope{}, err
	}
	if !exists {
		return profileScope{}, fmt.Errorf("namespace %q has no profile layer yet; run `dots namespace %s profiles edit` to create one", namespaceName, namespaceName)
	}
	m, err := manifest.Read(loc.Dir)
	if err != nil {
		return profileScope{}, err
	}
	pm, err := profile.Read(loc.Dir)
	if err != nil {
		return profileScope{}, err
	}
	s, err := state.Read()
	if err != nil {
		return profileScope{}, err
	}
	key := state.Key{Repo: loc.Repo.Name, Namespace: namespaceName}
	return profileScope{
		name:    namespaceName,
		dir:     loc.Dir,
		key:     key,
		entries: m.Entries,
		profile: pm,
		state:   s,
		active:  s.Entries[key].ActiveProfile,
	}, nil
}

// entryName maps a command-line token to the manifest entry it names. An
// entry is addressed by its name inside the namespace, but a destination
// path is accepted too, so the same spelling that tracked a file
// (`namespace <ns> add ~/.gitconfig`) also profiles it.
func (sc profileScope) entryName(token string) (string, error) {
	for _, e := range sc.entries {
		if e.Name == token {
			return e.Name, nil
		}
	}
	abs, err := filepath.Abs(token)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path for %s: %w", token, err)
	}
	for _, e := range sc.entries {
		if e.Dest == abs {
			return e.Name, nil
		}
	}
	return "", fmt.Errorf("%q is not tracked in namespace %q; only a tracked entry can be profiled", token, sc.name)
}

// relink applies newProfile to the namespace and reports every destination
// whose link target changed, as sub-lines under an operation line.
func (sc profileScope) relink(newProfile string) ([]string, error) {
	return engine.SwitchProfile(sc.key, sc.dir, sc.entries, sc.state, newProfile)
}

func reportProfile(marker, name string, relinked []string) {
	lines := []string{ui.Operation(marker, name, "")}
	for _, dest := range relinked {
		lines = append(lines, ui.Sub(ui.MarkerEnabled, dest, ""))
	}
	fmt.Print(ui.Report(lines, ""))
}

// editProfiles implements `namespace <ns> profiles edit`, the only thing
// that brings .profiles/.dots into existence (concept.md "The profile
// manifest > Creating it"). It builds its own scope rather than calling
// scopeFor, since scopeFor refuses when the file is absent and this is the
// one verb that must proceed anyway.
func editProfiles(namespaceName string, flags shared.Flags) error {
	loc, err := resolveNamespace(namespaceName, flags)
	if err != nil {
		return err
	}
	m, err := manifest.Read(loc.Dir)
	if err != nil {
		return err
	}
	pm, err := profile.Read(loc.Dir)
	if err != nil {
		return err
	}

	tracked := make([]string, len(m.Entries))
	for i, e := range m.Entries {
		tracked[i] = e.Name
	}
	seed := profile.EditBuffer(pm, tracked)

	return editBuffer(seed, profile.Path(loc.Dir),
		func(edited []byte) error {
			_, err := profile.Decode(edited)
			return err
		},
		func(seed, edited []byte) error {
			decoded, err := profile.Decode(edited)
			if err != nil {
				return fmt.Errorf("parse edited profile manifest: %w", err)
			}
			return profile.Write(loc.Dir, decoded)
		},
	)
}

// addProfile implements `profiles add <profile>`. Its success line carries
// a "created" detail rather than going through reportProfile bare: a bare
// "- <name>" is exactly what renderProfileList prints for that same profile
// once it exists, and a mutation that succeeds in silence — or that reads
// like a listing row instead of an announcement — is indistinguishable from
// one that did nothing (concept.md "Listing output").
func addProfile(namespaceName, name string, flags shared.Flags) error {
	sc, err := scopeFor(namespaceName, flags)
	if err != nil {
		return err
	}
	if err := profile.Create(sc.dir, name, flags.From); err != nil {
		return err
	}
	fmt.Print(ui.Report([]string{ui.Operation(ui.MarkerMaterialized, name, "created")}, ""))
	return nil
}

// rmProfile removes a profile. On the active one the namespace returns to
// main first, so the relink is reported and no destination is left pointing
// into a folder that is about to be trashed (concept.md "Teardown").
func rmProfile(namespaceName, name string, flags shared.Flags) error {
	sc, err := scopeFor(namespaceName, flags)
	if err != nil {
		return err
	}
	if !sc.profile.HasProfile(name) {
		return fmt.Errorf("no profile named %q in namespace %q", name, namespaceName)
	}

	var relinked []string
	if sc.active == name {
		relinked, err = sc.relink("")
		if err != nil {
			return err
		}
	}
	if err := profile.Remove(sc.dir, name); err != nil {
		return err
	}
	reportProfile(ui.MarkerRemoved, name, relinked)
	return nil
}

// mvProfile renames a profile. The active one's destinations point into the
// folder being renamed, so they are repointed afterwards — the rename
// changes what an entry links to even though it changes no content.
func mvProfile(namespaceName, oldName, newName string, flags shared.Flags) error {
	sc, err := scopeFor(namespaceName, flags)
	if err != nil {
		return err
	}
	if err := profile.Rename(sc.dir, oldName, newName); err != nil {
		return err
	}

	var relinked []string
	if sc.active == oldName {
		relinked, err = sc.relink(newName)
		if err != nil {
			return err
		}
	}
	reportProfile(ui.MarkerMaterialized, newName, relinked)
	return nil
}

// enableProfile switches to a profile. Enabling one that overrides nothing
// is legal and produces main's behaviour, warned rather than refused — an
// empty profile is usually one about to be filled (concept.md "Switching").
func enableProfile(namespaceName, name string, flags shared.Flags) error {
	sc, err := scopeFor(namespaceName, flags)
	if err != nil {
		return err
	}
	if !sc.profile.HasProfile(name) {
		return fmt.Errorf("no profile named %q in namespace %q", name, namespaceName)
	}

	overrides, err := profile.Overrides(sc.dir, name)
	if err != nil {
		return err
	}
	if len(overrides) == 0 {
		fmt.Fprintln(os.Stderr, ui.WarningTone(fmt.Sprintf("profile %q overrides nothing; every entry stays on main's version", name)))
	}

	relinked, err := sc.relink(name)
	if err != nil {
		return err
	}
	reportProfile(ui.MarkerEnabled, name, relinked)
	return nil
}

func disableProfile(namespaceName, name string, flags shared.Flags) error {
	sc, err := scopeFor(namespaceName, flags)
	if err != nil {
		return err
	}
	if sc.active != name {
		return fmt.Errorf("profile %q is not active in namespace %q", name, namespaceName)
	}
	relinked, err := sc.relink("")
	if err != nil {
		return err
	}
	reportProfile(ui.MarkerMaterialized, name, relinked)
	return nil
}

// returnToMain implements bare `<ns> main`, the second spelling of
// `profiles <profile> disable`: it names the layer being returned to rather
// than the profile being left (concept.md "main"), and is a no-op, not an
// error, when no profile is active.
func returnToMain(namespaceName string, flags shared.Flags) error {
	sc, err := scopeFor(namespaceName, flags)
	if err != nil {
		return err
	}
	if sc.active == "" {
		return nil
	}
	relinked, err := sc.relink("")
	if err != nil {
		return err
	}
	reportProfile(ui.MarkerMaterialized, profile.Main, relinked)
	return nil
}

// declareEntry implements `profiles main add <entry>`: only an entry the
// namespace manifest already tracks can be declared, because that manifest
// is what knows the destination (concept.md "Membership").
func declareEntry(namespaceName, token string, flags shared.Flags) error {
	sc, err := scopeFor(namespaceName, flags)
	if err != nil {
		return err
	}
	name, err := sc.entryName(token)
	if err != nil {
		return err
	}
	if err := profile.Declare(sc.dir, name); err != nil {
		return err
	}
	reportProfile(ui.MarkerMaterialized, name, nil)
	return nil
}

// undeclareEntry implements `profiles main rm <entry>`: removal is strictly
// bottom-up, so an entry profiles still override cannot be undeclared until
// those overrides are dropped. --force collapses the chain, trashing them
// (concept.md "Teardown").
func undeclareEntry(namespaceName, token string, flags shared.Flags) error {
	sc, err := scopeFor(namespaceName, flags)
	if err != nil {
		return err
	}
	name, err := sc.entryName(token)
	if err != nil {
		return err
	}

	holding, err := profile.OverridingProfiles(sc.dir, name)
	if err != nil {
		return err
	}
	if len(holding) > 0 && !flags.Force {
		commands := make([]string, len(holding))
		for i, p := range holding {
			commands[i] = fmt.Sprintf("dots namespace %s profiles %s rm %s", namespaceName, p, name)
		}
		return fmt.Errorf("%s", ui.List(
			fmt.Sprintf("%q is still overridden by %s:", name, ui.Plural(len(holding), "profile")),
			commands,
			"or --force to drop those overrides to the trash",
		))
	}

	for _, p := range holding {
		if err := profile.DropOverride(sc.dir, p, name); err != nil {
			return err
		}
	}

	var relinked []string
	if sc.active != "" {
		relinked, err = sc.relink(sc.active)
		if err != nil {
			return err
		}
	}
	if err := profile.Undeclare(sc.dir, name); err != nil {
		return err
	}
	reportProfile(ui.MarkerRemoved, name, relinked)
	return nil
}

// addOverride implements `profiles <profile> add <entry>`: the entry must be
// profiled already. When it is not, dots prompts to declare it and proceeds
// on y; non-interactively it errors naming `profiles main add <entry>`, and
// --force declares it without asking (concept.md "Profile level").
func addOverride(namespaceName, profileName, token string, flags shared.Flags) error {
	sc, err := scopeFor(namespaceName, flags)
	if err != nil {
		return err
	}
	if !sc.profile.HasProfile(profileName) {
		return fmt.Errorf("no profile named %q in namespace %q", profileName, namespaceName)
	}
	name, err := sc.entryName(token)
	if err != nil {
		return err
	}

	if !sc.profile.HasEntry(name) {
		if err := confirmDeclare(namespaceName, name, flags); err != nil {
			return err
		}
		if err := profile.Declare(sc.dir, name); err != nil {
			return err
		}
	}

	if err := profile.AddOverride(sc.dir, profileName, name); err != nil {
		return err
	}

	var relinked []string
	if sc.active == profileName {
		relinked, err = sc.relink(profileName)
		if err != nil {
			return err
		}
	}
	reportProfile(ui.MarkerMaterialized, name, relinked)
	return nil
}

// confirmDeclare resolves the undeclared-entry case for `profiles <profile>
// add`: --force declares silently, a terminal is asked, and anything else is
// an error naming the command that declares it explicitly.
func confirmDeclare(namespaceName, name string, flags shared.Flags) error {
	if flags.Force {
		return nil
	}
	if !ui.Interactive() {
		return fmt.Errorf("%q is not profiled; run `dots namespace %s profiles main add %s` first, or --force to declare it", name, namespaceName, name)
	}
	choice, err := ui.Prompt("", fmt.Sprintf("%q is not profiled yet. Declare it?", name), []string{"y", "N"})
	if err != nil {
		return err
	}
	if !strings.EqualFold(choice, "y") && !strings.EqualFold(choice, "yes") {
		return fmt.Errorf("cancelled")
	}
	return nil
}

// dropOverride implements `profiles <profile> rm <entry>`: one override
// goes to the trash and, when that profile is active, the destination
// relinks to the root copy (concept.md "Teardown").
func dropOverride(namespaceName, profileName, token string, flags shared.Flags) error {
	sc, err := scopeFor(namespaceName, flags)
	if err != nil {
		return err
	}
	name, err := sc.entryName(token)
	if err != nil {
		return err
	}
	if err := profile.DropOverride(sc.dir, profileName, name); err != nil {
		return err
	}

	var relinked []string
	if sc.active == profileName {
		relinked, err = sc.relink(profileName)
		if err != nil {
			return err
		}
	}
	reportProfile(ui.MarkerRemoved, name, relinked)
	return nil
}

// renderProfileList lists every profile the namespace offers, the active one
// marked with the listing alphabet's "+".
func renderProfileList(namespaceName string, flags shared.Flags) error {
	sc, err := scopeFor(namespaceName, flags)
	if err != nil {
		return err
	}
	rows := make([]ui.Entry, 0, len(sc.profile.Profiles))
	for _, p := range sc.profile.Profiles {
		marker := ui.MarkerMaterialized
		if p == sc.active {
			marker = ui.MarkerEnabled
		}
		rows = append(rows, ui.Entry{Marker: marker, Name: p})
	}
	renderListing(rows)
	return nil
}

// renderProfiledEntries lists the entries allowed to have overrides. One
// declared but no longer tracked by the namespace manifest is marked "!" —
// the same drift self-heal warns about, since both files are committed and
// can merge into disagreement with nobody at fault.
func renderProfiledEntries(namespaceName string, flags shared.Flags) error {
	sc, err := scopeFor(namespaceName, flags)
	if err != nil {
		return err
	}
	tracked := make(map[string]bool, len(sc.entries))
	for _, e := range sc.entries {
		tracked[e.Name] = true
	}
	rows := make([]ui.Entry, 0, len(sc.profile.Entries))
	for _, name := range sc.profile.Entries {
		marker := ui.MarkerMaterialized
		if !tracked[name] {
			marker = ui.MarkerProblem
		}
		rows = append(rows, ui.Entry{Marker: marker, Name: name})
	}
	renderListing(rows)
	return nil
}

// renderProfileEntries lists what one profile overrides. An override for an
// entry that is not declared profiled is marked "?": it is a file the
// resolution rule ignores until it is declared.
func renderProfileEntries(namespaceName, profileName string, flags shared.Flags) error {
	sc, err := scopeFor(namespaceName, flags)
	if err != nil {
		return err
	}
	if !sc.profile.HasProfile(profileName) {
		return fmt.Errorf("no profile named %q in namespace %q", profileName, namespaceName)
	}
	overrides, err := profile.Overrides(sc.dir, profileName)
	if err != nil {
		return err
	}
	rows := make([]ui.Entry, 0, len(overrides))
	for _, name := range overrides {
		marker := ui.MarkerMaterialized
		if !sc.profile.HasEntry(name) {
			marker = ui.MarkerUntracked
		}
		rows = append(rows, ui.Entry{Marker: marker, Name: name})
	}
	renderListing(rows)
	return nil
}
