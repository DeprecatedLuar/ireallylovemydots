package commands

import (
	gohelp "github.com/DeprecatedLuar/gohelp-luar"
)

// HandleHelp documents the canonical command form only. The verb-first and
// namespace-first aliases are discovered, not taught.
func HandleHelp(args []string) error {
	root := gohelp.NewPage("dots", "A dotfile manager for multiple dotfile repositories").
		Usage("dots <noun> [<name>] <verb> [args]").
		Section("Repository",
			gohelp.Item("repo add <url>", "Register a repository"),
			gohelp.Item("repo rm <repo>", "Deregister a repository"),
			gohelp.Item("repo list", "List registered repositories"),
			gohelp.Item("repo <repo> list", "List that repository's namespaces"),
		).
		Section("Namespace",
			gohelp.Item("namespace add <namespace>", "Create an empty namespace"),
			gohelp.Item("namespace rm <namespace>", "Remove a namespace"),
			gohelp.Item("namespace mv <namespace> <newname>", "Rename a namespace"),
			gohelp.Item("namespace list", "List every namespace across every repository"),
			gohelp.Item("namespace <ns> add <path>...", "Track files or directories"),
			gohelp.Item("namespace <ns> rm <path>...", "Untrack files or directories"),
			gohelp.Item("namespace <ns> list", "List that namespace's tracked entries"),
			gohelp.Item("namespace <ns> edit", "Edit its manifest in $EDITOR"),
			gohelp.Item("namespace <ns> enable", "Materialize and link"),
			gohelp.Item("namespace <ns> disable", "Remove symlinks, keep files"),
		).
		Section("Profiles",
			gohelp.Item("namespace <ns> profiles add <profile>", "Create a profile"),
			gohelp.Item("namespace <ns> profiles rm <profile>", "Remove a profile"),
			gohelp.Item("namespace <ns> profiles list", "List all profiles"),
			gohelp.Item("namespace <ns> profiles <profile> enable", "Switch to a profile"),
			gohelp.Item("namespace <ns> profiles <profile> add <file>", "Give a file a profile-specific variant"),
			gohelp.Item("namespace <ns> profiles <profile> rm <file>", "Drop the variant, falls back to base"),
			gohelp.Item("namespace <ns> profiles <profile> list", "List what this profile overrides"),
		).
		Section("Top level",
			gohelp.Item("sync", "Reconcile every registered repository"),
			gohelp.Item("list", "Every namespace, with state"),
		).
		Section("Flags",
			gohelp.Item("--repo", "Disambiguate a namespace name shared by several repositories"),
			gohelp.Item("--force", "Skip confirmation for a destructive default"),
			gohelp.Item("--purge", "Trash instead of restore on removal"),
			gohelp.Item("--yes", "Skip confirmation prompts"),
			gohelp.Item("--debug", "Verbose diagnostic output"),
		)

	gohelp.Run(append([]string{"help"}, args...), root)
	return nil
}
