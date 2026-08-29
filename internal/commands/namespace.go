package commands

import (
	"fmt"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/grammar"
	"github.com/DeprecatedLuar/dotz/internal/ui"
)

// HandleNamespace implements the namespace subtree: bare listing, the
// noun-level verbs that operate on a namespace by name in an argument
// (add, rm, mv), and per-namespace verbs reached by naming the namespace
// first (add, rm, list, edit, enable, disable, profiles).
//
// Tracking, linking, and removal are built in later phases; here each verb
// validates what phase 2 owns (reserved names, argument shape) and reports
// itself as not yet implemented.
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
		return renderNamespaceEntries(name)
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
		return errNotImplemented("namespace add")
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
		return errNotImplemented("namespace mv")
	case "list":
		return renderNamespaceList()
	default:
		return fmt.Errorf("namespace %s: not valid without a namespace name", verb)
	}
}

func handleNamespaceVerb(name, verb string, args []string, flags shared.Flags) error {
	switch verb {
	case "add":
		return errNotImplemented(fmt.Sprintf("namespace %s add", name))
	case "rm":
		return errNotImplemented(fmt.Sprintf("namespace %s rm", name))
	case "list":
		return renderNamespaceEntries(name)
	case "edit":
		return errNotImplemented(fmt.Sprintf("namespace %s edit", name))
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
		return errNotImplemented(fmt.Sprintf("namespace %s mv", name))
	default:
		return fmt.Errorf("unknown verb %q for namespace %q", verb, name)
	}
}

// renderNamespaceList lists every namespace across every registered
// repository. No repository or catalogue lookup exists yet (phases 3 and
// 4), so this always renders nothing, matching "success prints nothing".
func renderNamespaceList() error {
	fmt.Print(ui.Render(nil))
	return nil
}

func renderNamespaceEntries(name string) error {
	fmt.Print(ui.Render(nil))
	return nil
}

func errNotImplemented(what string) error {
	return fmt.Errorf("%s: not yet implemented", what)
}
