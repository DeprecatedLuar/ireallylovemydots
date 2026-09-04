// Package grammar holds the verb/noun vocabulary shared by the CLI router
// and the command handlers: the recursive grammar's fixed vocabulary, and
// the reserved-name set it implies.
package grammar

import (
	"slices"
	"sort"
)

// Verbs apply at every level of the grammar: repository, namespace, and
// profile. A name in verb position descends a level instead.
var Verbs = []string{"add", "rm", "mv", "list", "edit", "enable", "disable"}

// VerbAliases maps a short or alternate spelling to its canonical verb. See
// concept.md "Verb aliases": link names the mechanism instead of the intent
// and pairs with unlink, the one alias disable has.
var VerbAliases = map[string]string{
	"a":      "add",
	"remove": "rm",
	"move":   "mv",
	"ls":     "list",
	"e":      "edit",
	"link":   "enable",
	"unlink": "disable",
}

// Nouns introduce a subtree explicitly.
var Nouns = []string{"namespace", "repo", "profiles"}

// NounAliases maps a short or alternate spelling to its canonical noun. See
// concept.md "Profile level": profiles is the one noun with short spellings,
// since dots <ns> <profile> is the profile operation typed most often.
var NounAliases = map[string]string{
	"p":       "profiles",
	"profile": "profiles",
}

// TopOnly are reserved words that are neither verbs nor nouns but still
// occupy the top level: status aliases to list, sync has no target, doctor
// takes no name at all.
var TopOnly = []string{"status", "sync", "doctor"}

// RepoOnlyVerbs are verbs valid only at the repository level: init takes a
// local folder rather than a name, and has no meaning for a namespace or
// profile, unlike the shared Verbs set. adopt registers a clone already
// sitting in the data directory with no registry entry — concept.md "The
// data directory can drift from the registry too" — which is likewise
// meaningless below the repository level.
var RepoOnlyVerbs = []string{"init", "adopt"}

// NamespaceOnlyVerbs are verbs valid only at the namespace level: install
// and uninstall move a namespace between "in the repository" and "on this
// machine" (concept.md "Install and uninstall"), which has no meaning for a
// repository or profile. ignore and unignore declare a namespace explicitly
// out of dots' scope (concept.md "Namespace"), likewise meaningless above
// the namespace level.
var NamespaceOnlyVerbs = []string{"install", "uninstall", "ignore", "unignore"}

// IsVerb reports whether tok is one of the verbs valid at any level, in
// either its canonical or aliased spelling.
func IsVerb(tok string) bool {
	if slices.Contains(Verbs, tok) {
		return true
	}
	_, ok := VerbAliases[tok]
	return ok
}

// IsRepoVerb reports whether tok is valid in verb position within the repo
// subtree specifically: the shared verbs plus repo-only ones.
func IsRepoVerb(tok string) bool {
	return IsVerb(tok) || slices.Contains(RepoOnlyVerbs, tok)
}

// IsNamespaceVerb reports whether tok is valid in verb position within the
// namespace subtree specifically: the shared verbs plus install/uninstall.
func IsNamespaceVerb(tok string) bool {
	return IsVerb(tok) || slices.Contains(NamespaceOnlyVerbs, tok)
}

// Canonical returns tok's canonical verb spelling if tok is an alias,
// otherwise tok unchanged.
func Canonical(tok string) string {
	if canon, ok := VerbAliases[tok]; ok {
		return canon
	}
	return tok
}

// IsNoun reports whether tok explicitly introduces a subtree, in either its
// canonical or aliased spelling.
func IsNoun(tok string) bool {
	if slices.Contains(Nouns, tok) {
		return true
	}
	_, ok := NounAliases[tok]
	return ok
}

// CanonicalNoun returns tok's canonical noun spelling if tok is an alias,
// otherwise tok unchanged.
func CanonicalNoun(tok string) string {
	if canon, ok := NounAliases[tok]; ok {
		return canon
	}
	return tok
}

// IsReserved reports whether tok is any reserved word: a verb, a verb
// alias, a noun, a noun alias, a repo-only verb, or a top-only word.
// Reserved words cannot be used as namespace, repository, or profile names,
// because a name in verb position descends the grammar.
func IsReserved(tok string) bool {
	return IsVerb(tok) || IsNoun(tok) || slices.Contains(RepoOnlyVerbs, tok) ||
		slices.Contains(NamespaceOnlyVerbs, tok) || tok == "status" || tok == "sync" || tok == "doctor"
}

// ProfileMain is the name of the unprofiled layer — the namespace root. It
// is reserved at the profile level only: it takes the membership verbs and
// serves as a --from source, but no profile may be called it (concept.md
// "main"). A namespace or repository named "main" stays legal, since the
// name means nothing at those levels.
const ProfileMain = "main"

// IsReservedProfile reports whether tok is reserved as a profile name: every
// ordinary reserved word, plus main.
func IsReservedProfile(tok string) bool {
	return IsReserved(tok) || tok == ProfileMain
}

// Reserved returns every reserved word, sorted, for error messages.
func Reserved() []string {
	all := append([]string{}, Verbs...)
	for alias := range VerbAliases {
		all = append(all, alias)
	}
	all = append(all, Nouns...)
	for alias := range NounAliases {
		all = append(all, alias)
	}
	all = append(all, RepoOnlyVerbs...)
	all = append(all, NamespaceOnlyVerbs...)
	all = append(all, TopOnly...)
	sort.Strings(all)
	return all
}
