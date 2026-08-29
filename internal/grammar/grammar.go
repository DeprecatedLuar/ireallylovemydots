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

// VerbAliases maps a short or alternate spelling to its canonical verb.
// enable and disable have no alias. See concept.md "Verb aliases".
var VerbAliases = map[string]string{
	"a":      "add",
	"remove": "rm",
	"move":   "mv",
	"ls":     "list",
	"e":      "edit",
}

// Nouns introduce a subtree explicitly.
var Nouns = []string{"namespace", "repo", "profiles"}

// TopOnly are reserved words that are neither verbs nor nouns but still
// occupy the top level: status aliases to list, sync has no target.
var TopOnly = []string{"status", "sync"}

// IsVerb reports whether tok is one of the verbs valid at any level, in
// either its canonical or aliased spelling.
func IsVerb(tok string) bool {
	if slices.Contains(Verbs, tok) {
		return true
	}
	_, ok := VerbAliases[tok]
	return ok
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
// alias, a noun, or a top-only word. Reserved words cannot be used as
// namespace, repository, or profile names, because a name in verb position
// descends the grammar.
func IsReserved(tok string) bool {
	return IsVerb(tok) || IsNoun(tok) || tok == "status" || tok == "sync"
}

// Reserved returns every reserved word, sorted, for error messages.
func Reserved() []string {
	all := append([]string{}, Verbs...)
	for alias := range VerbAliases {
		all = append(all, alias)
	}
	all = append(all, Nouns...)
	all = append(all, TopOnly...)
	sort.Strings(all)
	return all
}
