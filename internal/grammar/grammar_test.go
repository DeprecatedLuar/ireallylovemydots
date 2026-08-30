package grammar

import (
	"slices"
	"testing"
)

func TestCanonical(t *testing.T) {
	cases := map[string]string{
		"a":      "add",
		"remove": "rm",
		"move":   "mv",
		"ls":     "list",
		"e":      "edit",
		"add":    "add",
		"enable": "enable",
	}
	for tok, want := range cases {
		if got := Canonical(tok); got != want {
			t.Errorf("Canonical(%q) = %q, want %q", tok, got, want)
		}
	}
}

func TestIsVerbRecognizesAliases(t *testing.T) {
	for alias := range VerbAliases {
		if !IsVerb(alias) {
			t.Errorf("IsVerb(%q) = false, want true", alias)
		}
	}
}

func TestReservedIncludesAliases(t *testing.T) {
	reserved := Reserved()
	for alias := range VerbAliases {
		if !slices.Contains(reserved, alias) {
			t.Errorf("Reserved() missing alias %q", alias)
		}
	}
}

func TestIsRepoVerbRecognizesInitButNotIsVerb(t *testing.T) {
	if IsVerb("init") {
		t.Error(`IsVerb("init") = true, want false: init is repo-only`)
	}
	if !IsRepoVerb("init") {
		t.Error(`IsRepoVerb("init") = false, want true`)
	}
	if !IsRepoVerb("add") {
		t.Error(`IsRepoVerb("add") = false, want true: shared verbs still count`)
	}
}

func TestInitIsReserved(t *testing.T) {
	if !IsReserved("init") {
		t.Error(`IsReserved("init") = false, want true`)
	}
	if !slices.Contains(Reserved(), "init") {
		t.Error(`Reserved() missing "init"`)
	}
}
