// Command dots is the entrypoint and sole router for dots. Flag extraction,
// token resolution, alias rewriting, ambiguity handling, and dispatch all
// live here - see CLAUDE.md's orchestrator pattern. internal/commands holds
// only command implementations; every other internal package is a
// single-responsibility primitive those implementations call into.
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/DeprecatedLuar/ireallylovemydots/internal/commands"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/commands/shared"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/grammar"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/selfheal"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/ui"
)

// version is stamped by the release workflow via -ldflags "-X
// main.version=<tag>". Left at devVersion otherwise, in which case
// printVersion falls back to the VCS stamp `go build` embeds.
var version = devVersion

// devVersion marks a binary built without a release stamp.
const devVersion = "dev"

// Build settings `go build` embeds from the git checkout it ran in.
const (
	vcsRevisionKey = "vcs.revision"
	vcsModifiedKey = "vcs.modified"
)

// shortRevisionLen matches git's default abbreviated commit length.
const shortRevisionLen = 7

// syncModeFlags are the long flags that pick a sync mode for one run.
var syncModeFlags = map[string]bool{
	"--merge": true, "--overlay": true, "--overwrite-local": true, "--overwrite-remote": true,
}

// target names which command subtree a resolved route dispatches into.
type target int

const (
	targetNamespace target = iota
	targetRepo
	targetList
	targetSync
	targetHelp
	targetDoctor
	targetCp
	targetMv
)

// route is the result of resolving argv into a dispatch target plus the
// arguments that target's handler expects.
type route struct {
	target target
	args   []string
}

func main() {
	if hasVersionFlag(os.Args[1:]) {
		printVersion()
		return
	}

	rawArgs, flags, err := extractGlobalFlags(os.Args[1:])
	if err != nil {
		die(err)
	}

	namespaces, err := namespaceNames()
	if err != nil {
		die(err)
	}
	repos, err := commands.RepoNames()
	if err != nil {
		die(err)
	}

	r, err := resolveRoute(rawArgs, namespaces, repos, ambiguityChooser)
	if err != nil {
		die(err)
	}

	// Self-heal runs once here, ahead of every dispatch target, so no
	// command handler has to remember to call it, per concept.md
	// "Self-healing": it fires on every invocation. Its Problems feed
	// listing's markers by rereading the filesystem there, not by being
	// threaded through; self-heal itself never prints. Reconciliation
	// happens now, before the command reads anything, but its own findings
	// print after dispatch, once the command's own output is on the
	// screen, so a warning about the data directory as a whole reads as a
	// footer under the thing it explains rather than noise ahead of it.
	findings, err := selfheal.Run()
	if err != nil {
		die(err)
	}

	switch r.target {
	case targetNamespace:
		err = commands.HandleNamespace(r.args, flags, findings)
	case targetRepo:
		err = commands.HandleRepo(r.args, flags)
	case targetList:
		err = commands.HandleList(r.args, findings)
	case targetSync:
		err = commands.HandleSync(r.args, flags)
	case targetHelp:
		err = commands.HandleHelp(r.args)
	case targetDoctor:
		err = commands.HandleDoctor(r.args, findings)
	case targetCp:
		err = commands.HandleCp(r.args, flags)
	case targetMv:
		err = commands.HandleMv(r.args, flags)
	}
	commands.RenderSelfHealFindings(findings)
	if err != nil {
		if errors.Is(err, commands.ErrSomeSkipped) {
			os.Exit(1)
		}
		die(err)
	}
}

// shortFlags maps every valueless long flag's single-letter short form to
// the field it sets, per concept.md "Flags". -r/--repo takes a value and is
// handled separately - it never joins a cluster.
var shortFlags = map[byte]func(*shared.Flags){
	'A': func(f *shared.Flags) { f.All = true },
	'f': func(f *shared.Flags) { f.Force = true },
	'p': func(f *shared.Flags) { f.Purge = true },
	'y': func(f *shared.Flags) { f.Yes = true },
	'd': func(f *shared.Flags) { f.Debug = true },
	'i': func(f *shared.Flags) { f.Install = true },
}

// extractGlobalFlags pulls the command-wide flags from anywhere in args and
// returns the remaining positional tokens. Every valueless long flag has a
// short form, and short forms cluster: -Af is --all --force. An unknown
// letter in a cluster is an error naming it - never reinterpreted as a
// positional name, per concept.md "Flags".
func extractGlobalFlags(args []string) ([]string, shared.Flags, error) {
	var remaining []string
	var flags shared.Flags
	skipNext := false

	for i, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}

		switch {
		case arg == "--all":
			flags.All = true
		case arg == "--force":
			flags.Force = true
		case arg == "--purge":
			flags.Purge = true
		case arg == "--yes":
			flags.Yes = true
		case syncModeFlags[arg]:
			mode := strings.TrimPrefix(arg, "--")
			if flags.SyncMode != "" && flags.SyncMode != mode {
				return nil, shared.Flags{}, fmt.Errorf("--%s and %s are contradictory; pick one", flags.SyncMode, arg)
			}
			flags.SyncMode = mode
		case arg == "--debug":
			flags.Debug = true
		case arg == "--bootstrap":
			flags.Bootstrap = true
		case arg == "--install":
			flags.Install = true
		case arg == "--restore":
			flags.Restore = true
		case arg == "--repo" || arg == "-r":
			if i+1 >= len(args) {
				return nil, shared.Flags{}, fmt.Errorf("%s requires a value", arg)
			}
			flags.Repo = args[i+1]
			skipNext = true
		case strings.HasPrefix(arg, "--repo="):
			flags.Repo = strings.TrimPrefix(arg, "--repo=")
		case arg == "--from":
			if i+1 >= len(args) {
				return nil, shared.Flags{}, fmt.Errorf("%s requires a value", arg)
			}
			flags.From = args[i+1]
			skipNext = true
		case strings.HasPrefix(arg, "--from="):
			flags.From = strings.TrimPrefix(arg, "--from=")
		case len(arg) > 1 && arg[0] == '-' && arg[1] != '-':
			if err := applyShortCluster(arg[1:], &flags); err != nil {
				return nil, shared.Flags{}, err
			}
		default:
			remaining = append(remaining, arg)
		}
	}

	// "--force" implies "--yes"; "--yes" never implies "--force" (concept.md
	// "Flags"). Wired here once so no command has to duplicate the
	// implication.
	if flags.Force {
		flags.Yes = true
	}

	return remaining, flags, nil
}

// applyShortCluster applies every letter of a short-flag cluster (the
// characters of "-Af" after the leading dash) to flags. Clustering stops at
// the first letter that is not a known valueless short flag, and the whole
// token is an error naming that letter.
func applyShortCluster(letters string, flags *shared.Flags) error {
	for i := 0; i < len(letters); i++ {
		set, ok := shortFlags[letters[i]]
		if !ok {
			return fmt.Errorf("unknown flag letter %q in -%s", letters[i], letters)
		}
		set(flags)
	}
	return nil
}

// resolveRoute implements concept.md "Name resolution" and "Aliases":
// verb, then noun, then namespace name, then repository name, with the
// verb-first and namespace-first spellings rewritten to the canonical
// `namespace <name> <verb>` form before dispatch. ambiguous is called only
// when a bare token matches both a namespace and a repository; it must
// return "namespace" or "repo".
func resolveRoute(args []string, namespaces, repos []string, ambiguous func(name string) (string, error)) (route, error) {
	if len(args) == 0 {
		return route{target: targetList}, nil
	}

	tok0 := args[0]
	switch tok0 {
	case "namespace":
		return route{target: targetNamespace, args: args[1:]}, nil
	case "repo":
		return route{target: targetRepo, args: args[1:]}, nil
	case "init":
		// `dots init [path]` -> `dots repo init [path]`. Safe as a bare
		// top-level alias because "init" is reserved (grammar.RepoOnlyVerbs),
		// so no namespace or repository can ever be named "init".
		return route{target: targetRepo, args: append([]string{"init"}, args[1:]...)}, nil
	case "config", "conf":
		return route{target: targetRepo, args: append([]string{"edit"}, args[1:]...)}, nil
	case "sync":
		return route{target: targetSync, args: args[1:]}, nil
	case "cp", "copy":
		return route{target: targetCp, args: args[1:]}, nil
	case "mv", "move":
		return route{target: targetMv, args: args[1:]}, nil
	case "list", "ls", "status":
		return route{target: targetList, args: args[1:]}, nil
	case "doctor":
		return route{target: targetDoctor, args: args[1:]}, nil
	case "help", "h", "-h", "--help":
		return route{target: targetHelp, args: args[1:]}, nil
	}

	// Verb-first alias: `dots enable neovim` -> `namespace enable neovim`.
	// This alias only ever targets the namespace subtree-repo has its
	// own explicit `repo add`/`repo rm`, never a bare verb form. Per
	// concept.md "Aliases", "the verb-first alias always resolves to the
	// collection level, whatever follows it"-the collection level is
	// where a namespace not yet on this machine is still nameable, which is
	// what makes `dots install <ns>` and `dots enable <ns> -i` reach the
	// namespaces they exist to fetch. `add` is the sole verb whose
	// collection meaning takes exactly one name; routing it here the same
	// way as every other verb still produces the right usage error for
	// `dots add <ns> <path>`, via handleNamespaceNounVerb's own arity
	// check-no special case needed.
	if grammar.IsVerb(tok0) || slices.Contains(grammar.NamespaceOnlyVerbs, tok0) {
		canon := grammar.Canonical(tok0)
		return route{target: targetNamespace, args: append([]string{canon}, args[1:]...)}, nil
	}

	// A namespace spec (repo/ns, owner/repo/ns) can only name a namespace.
	if strings.Contains(tok0, "/") {
		return route{target: targetNamespace, args: args}, nil
	}

	// Bare name: namespace-first alias, or the equivalent repo shortcut.
	inNS := contains(namespaces, tok0)
	inRepo := contains(repos, tok0)
	switch {
	case inNS && inRepo:
		choice, err := ambiguous(tok0)
		if err != nil {
			return route{}, err
		}
		switch choice {
		case "namespace":
			return route{target: targetNamespace, args: args}, nil
		case "repo":
			return route{target: targetRepo, args: args}, nil
		default:
			return route{}, fmt.Errorf("internal error: unrecognized disambiguation choice %q", choice)
		}
	case inNS:
		return route{target: targetNamespace, args: args}, nil
	case inRepo:
		return route{target: targetRepo, args: args}, nil
	default:
		return route{}, &tipError{
			err: fmt.Errorf("That's not a real command: %s", tok0),
			tip: "Try 'dots help' for usage",
		}
	}
}

// tipError pairs a fatal error with a follow-up tip line, printed dim
// beneath the arrow-toned error line by die. Not every error carries one —
// only cases where a next step is obvious, such as mistyping a command.
type tipError struct {
	err error
	tip string
}

func (e *tipError) Error() string { return e.err.Error() }
func (e *tipError) Unwrap() error { return e.err }

func contains(list []string, name string) bool {
	for _, v := range list {
		if strings.EqualFold(v, name) {
			return true
		}
	}
	return false
}

// ambiguityChooser resolves a token that matches both a namespace and a
// repository. This is a different layer of ambiguity than
// namespace.Resolve's: here the token itself is read two ways (as a
// namespace or as a repository name), not one namespace name held by
// several repositories. That second kind can still be nested inside the
// first-the token might match a namespace that itself exists in more
// than one repository-and per concept.md "Name resolution" a name
// matching more than one existing thing always errors, naming every
// candidate, and never prompts even interactively; so that case is checked
// first and short-circuits the namespace/repository choice entirely.
// Otherwise it prompts interactively, or errors listing both readings when
// not connected to a terminal.
func ambiguityChooser(name string) (string, error) {
	nsRepos, err := commands.NamespaceRepoCandidates(name)
	if err != nil {
		return "", err
	}
	if len(nsRepos) > 1 {
		specs := make([]string, len(nsRepos))
		for i, r := range nsRepos {
			specs[i] = r + "/" + name
		}
		return "", fmt.Errorf("%q names a namespace in %d repositories (%s); name one, e.g. `%s`", name, len(nsRepos), strings.Join(specs, ", "), specs[0])
	}
	if !ui.Interactive() {
		return "", fmt.Errorf("%q matches both a namespace and a repository; use --repo to disambiguate or rename one", name)
	}
	resp, err := ui.Prompt(
		"",
		fmt.Sprintf("%q matches both a namespace and a repository. Which did you mean?", name),
		[]string{"[n] namespace", "[r] repository"},
	)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(strings.TrimSpace(resp)) {
	case "n", "namespace":
		return "namespace", nil
	case "r", "repo", "repository":
		return "repo", nil
	default:
		return "", fmt.Errorf("unrecognized choice %q", resp)
	}
}

// namespaceNames returns every namespace name materialized locally across
// every registered repository, for router ambiguity resolution.
func namespaceNames() ([]string, error) {
	return commands.NamespaceNames()
}

// hasVersionFlag reports whether --version appears anywhere in args. It is
// checked ahead of extractGlobalFlags and route resolution, per concept.md
// "Flags", since printing the build stamp should never depend on the state
// of any registered repository or namespace.
func hasVersionFlag(args []string) bool {
	for _, a := range args {
		if a == "--version" {
			return true
		}
	}
	return false
}

// printVersion reports what dots was built from and whether the tree was
// dirty at build time, per concept.md "Flags" and "Top level".
func printVersion() {
	stamp, dirty := buildStamp()
	if dirty {
		fmt.Printf("dots %s (dirty)\n", stamp)
		return
	}
	fmt.Printf("dots %s\n", stamp)
}

// buildStamp returns the release tag when one was stamped, otherwise the
// commit and dirty state recorded by `go build`. A binary built without
// VCS info (`go run`, outside a checkout) reports devVersion.
func buildStamp() (stamp string, dirty bool) {
	if version != devVersion {
		return version, false
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return version, false
	}
	stamp = version
	for _, setting := range info.Settings {
		switch setting.Key {
		case vcsRevisionKey:
			stamp = setting.Value
			if len(stamp) > shortRevisionLen {
				stamp = stamp[:shortRevisionLen]
			}
		case vcsModifiedKey:
			dirty = setting.Value == "true"
		}
	}
	return stamp, dirty
}

// die prints err set off by a leading blank line, so it reads as its own
// block rather than running into whatever output preceded it. It prints as
// an arrow-marked line, the same tone used for "!" markers elsewhere,
// rather than an "Error:" prefix. When err is a *tipError, a dim follow-up
// line is appended below it.
func die(err error) {
	msg := err.Error()
	fmt.Fprintf(os.Stderr, "\n%s\n", ui.ErrorTone(fmt.Sprintf("%s %s", ui.Arrow, msg)))
	if te, ok := err.(*tipError); ok {
		fmt.Fprintf(os.Stderr, "%s\n", ui.Tip(te.tip))
	}
	os.Exit(1)
}
