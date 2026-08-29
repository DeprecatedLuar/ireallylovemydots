package commands

import (
	"fmt"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/grammar"
	"github.com/DeprecatedLuar/dotz/internal/ui"
)

// handleProfiles implements the profiles subtree of one namespace: bare
// listing, the noun-level verbs that operate on a profile by name in an
// argument (add, rm), and per-profile verbs reached by naming the profile
// first (enable, add, rm, list).
//
// Profile storage and the symlink chain are built in phase 8; here each
// verb validates argument shape and reports itself as not yet implemented.
func handleProfiles(namespace string, args []string, flags shared.Flags) error {
	if len(args) == 0 {
		return renderProfileList(namespace)
	}

	if grammar.IsVerb(args[0]) {
		return handleProfilesNounVerb(namespace, grammar.Canonical(args[0]), args[1:])
	}

	profile := args[0]
	rest := args[1:]
	if len(rest) == 0 {
		return fmt.Errorf("usage: namespace %s profiles %s <enable|add|rm|list>", namespace, profile)
	}

	switch grammar.Canonical(rest[0]) {
	case "enable":
		return errNotImplemented(fmt.Sprintf("namespace %s profiles %s enable", namespace, profile))
	case "add":
		if len(rest) != 2 {
			return fmt.Errorf("usage: namespace %s profiles %s add <file>", namespace, profile)
		}
		return errNotImplemented(fmt.Sprintf("namespace %s profiles %s add", namespace, profile))
	case "rm":
		if len(rest) != 2 {
			return fmt.Errorf("usage: namespace %s profiles %s rm <file>", namespace, profile)
		}
		return errNotImplemented(fmt.Sprintf("namespace %s profiles %s rm", namespace, profile))
	case "list":
		return renderProfileEntries(namespace, profile)
	default:
		return fmt.Errorf("unknown verb %q for namespace %q profile %q", rest[0], namespace, profile)
	}
}

func handleProfilesNounVerb(namespace, verb string, args []string) error {
	switch verb {
	case "add":
		if len(args) != 1 {
			return fmt.Errorf("usage: namespace %s profiles add <profile>", namespace)
		}
		name := args[0]
		if grammar.IsReserved(name) {
			return fmt.Errorf("%q is a reserved name and cannot be used for a profile", name)
		}
		return errNotImplemented(fmt.Sprintf("namespace %s profiles add", namespace))
	case "rm":
		if len(args) != 1 {
			return fmt.Errorf("usage: namespace %s profiles rm <profile>", namespace)
		}
		return errNotImplemented(fmt.Sprintf("namespace %s profiles rm", namespace))
	case "list":
		return renderProfileList(namespace)
	default:
		return fmt.Errorf("namespace %s profiles %s: not valid without a profile name", namespace, verb)
	}
}

func renderProfileList(namespace string) error {
	fmt.Print(ui.Render(nil))
	return nil
}

func renderProfileEntries(namespace, profile string) error {
	fmt.Print(ui.Render(nil))
	return nil
}
