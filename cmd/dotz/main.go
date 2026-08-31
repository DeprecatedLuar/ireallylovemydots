// Command dotz is the entrypoint and sole router for dots. Flag extraction,
// token resolution, alias rewriting, ambiguity handling, and dispatch all
// live here — see CLAUDE.md's orchestrator pattern. internal/commands holds
// only command implementations; every other internal package is a
// single-responsibility primitive those implementations call into.
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/DeprecatedLuar/dotz/internal/commands"
	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/grammar"
	"github.com/DeprecatedLuar/dotz/internal/ui"
)

// version is stamped at build time via -ldflags "-X main.version=...", per
// build.sh, using `git describe --always --dirty`. Left at its default when
// built any other way (`go build ./...`, `go run`), so an unstamped binary
// still identifies itself as such rather than lying about its provenance.
var version = "dev"

// versionDirtySuffix is git describe --dirty's own marker; checking for it
// avoids needing a second ldflags variable to carry a dirty bit.
const versionDirtySuffix = "-dirty"

// target names which command subtree a resolved route dispatches into.
type target int

const (
	targetNamespace target = iota
	targetRepo
	targetList
	targetSync
	targetHelp
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

	switch r.target {
	case targetNamespace:
		err = commands.HandleNamespace(r.args, flags)
	case targetRepo:
		err = commands.HandleRepo(r.args, flags)
	case targetList:
		err = commands.HandleList(r.args)
	case targetSync:
		err = commands.HandleSync(r.args)
	case targetHelp:
		err = commands.HandleHelp(r.args)
	}
	if err != nil {
		if errors.Is(err, commands.ErrSomeSkipped) {
			os.Exit(1)
		}
		die(err)
	}
}

// shortFlags maps every valueless long flag's single-letter short form to
// the field it sets, per concept.md "Flags". -r/--repo takes a value and is
// handled separately — it never joins a cluster.
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
// letter in a cluster is an error naming it — never reinterpreted as a
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
	case "sync":
		return route{target: targetSync, args: args[1:]}, nil
	case "list", "ls", "status":
		return route{target: targetList, args: args[1:]}, nil
	case "help", "h", "-h", "--help":
		return route{target: targetHelp, args: args[1:]}, nil
	}

	// Verb-first alias: `dots enable neovim` -> `namespace neovim enable`.
	// This alias only ever targets the namespace subtree — repo has its
	// own explicit `repo add`/`repo rm`, never a bare verb form.
	if grammar.IsVerb(tok0) || slices.Contains(grammar.NamespaceOnlyVerbs, tok0) {
		canon := grammar.Canonical(tok0)
		// enable, install, and uninstall all accept more than one namespace
		// name (concept.md "Any verb that takes a namespace takes several");
		// every other verb stays single-name, so only these three are
		// routed to the noun-level batch form here.
		batchable := canon == "enable" || canon == "install" || canon == "uninstall"
		if batchable && len(args) == 1 {
			return route{target: targetNamespace, args: []string{canon}}, nil
		}
		if batchable && len(args) > 2 {
			return route{target: targetNamespace, args: append([]string{canon}, args[1:]...)}, nil
		}
		if len(args) < 2 {
			return route{}, fmt.Errorf("usage: %s <namespace> [args]", tok0)
		}
		name := args[1]
		rewritten := append([]string{name, canon}, args[2:]...)
		return route{target: targetNamespace, args: rewritten}, nil
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
		return route{}, fmt.Errorf("unknown command or name %q", tok0)
	}
}

func contains(list []string, name string) bool {
	for _, v := range list {
		if strings.EqualFold(v, name) {
			return true
		}
	}
	return false
}

// ambiguityChooser prompts interactively, or errors listing both
// candidates when not connected to a terminal.
func ambiguityChooser(name string) (string, error) {
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

// printVersion reports the commit dots was built from and whether the tree
// was dirty at build time, per concept.md "Flags" and "Top level". Both come
// from a single string stamped by build.sh via -ldflags -X main.version=...
// (git describe --always --dirty), so the dirty state is read back off its
// "-dirty" suffix rather than carried as a second variable.
func printVersion() {
	commit := strings.TrimSuffix(version, versionDirtySuffix)
	if commit != version {
		fmt.Printf("dots %s (dirty)\n", commit)
		return
	}
	fmt.Printf("dots %s\n", commit)
}

// die prints err set off by a blank line on each side, so it reads as its
// own block rather than running into whatever output preceded it or the
// shell prompt that follows.
func die(err error) {
	fmt.Fprintf(os.Stderr, "\n%s\n\n", ui.ErrorTone(fmt.Sprintf("Error: %v", err)))
	os.Exit(1)
}
