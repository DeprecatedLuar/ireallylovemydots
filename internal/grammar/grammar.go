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

// TopOnly are reserved words that are neither verbs nor nouns but still
// occupy the top level: status aliases to list, sync has no target.
var TopOnly = []string{"status", "sync"}

// RepoOnlyVerbs are verbs valid only at the repository level: init takes a
// local folder rather than a name, and has no meaning for a namespace or
// profile, unlike the shared Verbs set.
var RepoOnlyVerbs = []string{"init"}

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

// Canonical returns tok's canonical verb spelling if tok is an alias,
// otherwise tok unchanged.
func Canonical(tok string) string {
	if canon, ok := VerbAliases[tok]; ok {
		return canon
	}
	return tok
}

// IsNoun reports whether tok explicitly introduces a subtree.
func IsNoun(tok string) bool {
	return slices.Contains(Nouns, tok)
}

// IsReserved reports whether tok is any reserved word: a verb, a verb
// alias, a noun, a repo-only verb, or a top-only word. Reserved words cannot
// be used as namespace, repository, or profile names, because a name in
// verb position descends the grammar.
func IsReserved(tok string) bool {
	return IsVerb(tok) || IsNoun(tok) || slices.Contains(RepoOnlyVerbs, tok) || tok == "status" || tok == "sync"
}

// Reserved returns every reserved word, sorted, for error messages.
func Reserved() []string {
	all := append([]string{}, Verbs...)
	for alias := range VerbAliases {
		all = append(all, alias)
	}
	all = append(all, Nouns...)
	all = append(all, RepoOnlyVerbs...)
	all = append(all, TopOnly...)
	sort.Strings(all)
	return all
}
