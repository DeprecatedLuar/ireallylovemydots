// Command dotz is the entrypoint and sole router for dots. Flag extraction,
// token resolution, alias rewriting, ambiguity handling, and dispatch all
// live here — see CLAUDE.md's orchestrator pattern. internal/commands holds
// only command implementations; every other internal package is a
// single-responsibility primitive those implementations call into.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/DeprecatedLuar/dotz/internal/commands"
	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/grammar"
	"github.com/DeprecatedLuar/dotz/internal/ui"
)

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
	rawArgs, flags := extractGlobalFlags(os.Args[1:])

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
		die(err)
	}
}

// extractGlobalFlags pulls --repo, --force, --purge, --yes, and --debug
// from anywhere in args and returns the remaining positional tokens.
func extractGlobalFlags(args []string) ([]string, shared.Flags) {
	var remaining []string
	var flags shared.Flags
	skipNext := false

	for i, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}

		switch {
		case arg == "--force":
			flags.Force = true
		case arg == "--purge":
			flags.Purge = true
		case arg == "--yes":
			flags.Yes = true
		case arg == "--debug":
			flags.Debug = true
		case arg == "--repo":
			if i+1 < len(args) {
				flags.Repo = args[i+1]
				skipNext = true
			}
		case strings.HasPrefix(arg, "--repo="):
			flags.Repo = strings.TrimPrefix(arg, "--repo=")
		default:
			remaining = append(remaining, arg)
		}
	}

	return remaining, flags
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
	if grammar.IsVerb(tok0) {
		if len(args) < 2 {
			return route{}, fmt.Errorf("usage: %s <namespace> [args]", tok0)
		}
		name := args[1]
		rewritten := append([]string{name, grammar.Canonical(tok0)}, args[2:]...)
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

func die(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}
