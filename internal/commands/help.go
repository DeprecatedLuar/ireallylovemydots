package commands

import (
	gohelp "github.com/DeprecatedLuar/gohelp-luar"
)

// HandleHelp documents the canonical command form, plus the bare shortcuts
// that resolve to it. Namespace and profile subcommands each live on their
// own page (`dots help namespace`, `dots help profiles`) since they are
// subtrees of their own.
func HandleHelp(args []string) error {
	root := gohelp.NewPage("dots", "I really love my dots").
		Usage("dots <noun> [<name>] <verb> [args]").
		Text("Manages multiple dotfile git repositories, each holding namespaces.").
		Section("Repository",
			gohelp.Item("repo add <url>", "Register a repository"),
			gohelp.Item("repo init [path], init [path]", "Register a local folder, no remote"),
			gohelp.Item("repo adopt <name>", "Register a clone already in the data directory"),
			gohelp.Item("repo rm <repo>", "Deregister a repository"),
			gohelp.Item("repo list", "List registered repositories"),
			gohelp.Item("repo <repo> list", "List that repository's namespaces"),
		).
		Section("Top level",
			gohelp.Item("sync", "Reconcile every registered repository"),
			gohelp.Item("list, ls, status", "Every namespace, with state"),
			gohelp.Item("enable <namespace>...", "Materialize and link"),
			gohelp.Item("disable <namespace>", "Remove symlinks, keep files"),
			gohelp.Item("install <namespace>...", "Put its files on disk, link nothing"),
			gohelp.Item("uninstall <namespace>...", "Take its files off disk, keep it tracked"),
			gohelp.Item("add <namespace> <path>...", "Track files or directories"),
			gohelp.Item("rm <namespace> <path>...", "Untrack files or directories"),
			gohelp.Item("mv <namespace> <newname>", "Rename a namespace"),
			gohelp.Item("edit <namespace>", "Edit its manifest in $EDITOR"),
			gohelp.Item("<namespace> <verb> [args]", "same, verb last"),
		).
		Section("Flags",
			gohelp.Item("-A, --all", "Enable every disabled namespace"),
			gohelp.Item("--repo", "Disambiguate a namespace name shared by several repositories"),
			gohelp.Item("--force", "Skip confirmation for a destructive default"),
			gohelp.Item("--purge", "Trash instead of restore on removal"),
			gohelp.Item("--yes", "Skip confirmation prompts"),
			gohelp.Item("--debug", "Verbose diagnostic output"),
		)

	namespacePage := gohelp.NewPage("namespace", "Namespace commands").
		Text("A named bundle of tracked files that all link or unlink together.").
		Section("Manage namespaces",
			gohelp.Item("namespace add <namespace>", "Create an empty namespace"),
			gohelp.Item("namespace rm <namespace>", "Remove a namespace"),
			gohelp.Item("namespace mv <namespace> <newname>", "Rename a namespace"),
			gohelp.Item("namespace list", "List every namespace across every repository"),
		).
		Section("Namespace contents",
			gohelp.Item("namespace <ns> add <path>...", "Track files or directories"),
			gohelp.Item("namespace <ns> rm <path>...", "Untrack files or directories"),
			gohelp.Item("namespace <ns> list", "List that namespace's tracked entries"),
			gohelp.Item("namespace <ns> edit", "Edit its manifest in $EDITOR"),
		).
		Section("Namespace state",
			gohelp.Item("namespace <ns> enable", "Materialize and link"),
			gohelp.Item("namespace <ns> disable", "Remove symlinks, keep files"),
			gohelp.Item("namespace <ns> install", "Put its files on disk, link nothing"),
			gohelp.Item("namespace <ns> uninstall", "Take its files off disk, keep it tracked"),
		).
		Section("Out of scope",
			gohelp.Item("namespace ignore", "List every ignored namespace"),
			gohelp.Item("namespace ignore <ns>", "Declare it out of dots' scope; invisible to listing and self-heal"),
			gohelp.Item("namespace unignore <ns>", "Undo namespace ignore"),
		)

	profilesPage := gohelp.NewPage("profiles", "Profile commands").
		Text("Per-entry variants within a namespace: main is the namespace root, and the "+
			"active profile's overrides sit on top of it, one entry at a time.").
		Section("Manage profiles",
			gohelp.Item("namespace <ns> profiles list", "List all profiles, active marked"),
			gohelp.Item("namespace <ns> profiles add <profile>", "Create a profile, empty"),
			gohelp.Item("namespace <ns> profiles add <profile> --from <source>", "Create it seeded from main or another profile"),
			gohelp.Item("namespace <ns> profiles rm <profile>", "Remove a profile"),
			gohelp.Item("namespace <ns> profiles mv <profile> <new>", "Rename a profile"),
			gohelp.Item("namespace <ns> profiles <profile> enable", "Switch to a profile"),
			gohelp.Item("namespace <ns> profiles <profile> disable", "Back to main"),
		).
		Section("Membership",
			gohelp.Item("namespace <ns> profiles main list", "Entries allowed to have overrides"),
			gohelp.Item("namespace <ns> profiles main add <entry>", "Declare an entry profiled"),
			gohelp.Item("namespace <ns> profiles main rm <entry>", "Undeclare it"),
		).
		Section("Profile contents",
			gohelp.Item("namespace <ns> profiles <profile> add <entry>", "Override this entry here"),
			gohelp.Item("namespace <ns> profiles <profile> rm <entry>", "Drop the override, back to main"),
			gohelp.Item("namespace <ns> profiles <profile> list", "List what this profile overrides"),
		).
		Section("Shorthand",
			gohelp.Item("<ns> p, <ns> profile", "Same as <ns> profiles"),
			gohelp.Item("<ns> <profile>", "Switch to it"),
			gohelp.Item("<ns> <profile> ls", "What this profile overrides"),
			gohelp.Item("<ns> <profile> add|rm <entry>", "Override or drop the override"),
			gohelp.Item("<ns> <profile> disable, <ns> main", "Back to main"),
		)

	gohelp.Run(append([]string{"help"}, args...), root, namespacePage, profilesPage)
	return nil
}
