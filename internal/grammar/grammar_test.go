package grammar

import (
	"slices"
	"testing"
)

func TestCanonical(t *testing.T) {
	cases := map[string]string{
		"a":      "add",
		"remove": "rm",
		"rename": "rn",
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

func TestIsNamespaceVerbRecognizesInstallUninstallButNotIsVerb(t *testing.T) {
	for _, tok := range []string{"install", "uninstall"} {
		if IsVerb(tok) {
			t.Errorf(`IsVerb(%q) = true, want false: namespace-only`, tok)
		}
		if !IsNamespaceVerb(tok) {
			t.Errorf(`IsNamespaceVerb(%q) = false, want true`, tok)
		}
	}
	if !IsNamespaceVerb("add") {
		t.Error(`IsNamespaceVerb("add") = false, want true: shared verbs still count`)
	}
}

func TestInstallUninstallAreReserved(t *testing.T) {
	for _, tok := range []string{"install", "uninstall"} {
		if !IsReserved(tok) {
			t.Errorf(`IsReserved(%q) = false, want true`, tok)
		}
		if !slices.Contains(Reserved(), tok) {
			t.Errorf("Reserved() missing %q", tok)
		}
	}
}

func TestRestoreIsNamespaceOnlyAndReserved(t *testing.T) {
	if IsVerb("restore") {
		t.Error(`IsVerb("restore") = true, want false: namespace-only`)
	}
	if !IsNamespaceVerb("restore") {
		t.Error(`IsNamespaceVerb("restore") = false, want true`)
	}
	if !IsReserved("restore") {
		t.Error(`IsReserved("restore") = false, want true`)
	}
	if !slices.Contains(Reserved(), "restore") {
		t.Error(`Reserved() missing "restore"`)
	}
}

func TestCpAndCopyAreReserved(t *testing.T) {
	for _, tok := range []string{"cp", "copy"} {
		if !IsReserved(tok) {
			t.Errorf(`IsReserved(%q) = false, want true`, tok)
		}
		if !slices.Contains(Reserved(), tok) {
			t.Errorf("Reserved() missing %q", tok)
		}
	}
}

// TestRenameResolvesToRn covers implementation-plan.md Phase 17: the
// long-form rename alias canonicalizes to the shared rn verb.
func TestRenameResolvesToRn(t *testing.T) {
	if got := Canonical("rename"); got != "rn" {
		t.Errorf(`Canonical("rename") = %q, want "rn"`, got)
	}
}

// TestMoveResolvesToMv covers implementation-plan.md Phase 17: move is mv's
// top-level alias, the same relationship copy has to cp — both spellings are
// reserved top-only words with no per-level verb meaning.
func TestMoveResolvesToMv(t *testing.T) {
	if !slices.Contains(TopOnly, "mv") || !slices.Contains(TopOnly, "move") {
		t.Fatal(`expected "mv" and "move" to both be top-level words`)
	}
}

// TestMvMoveRnRenameAreReserved covers implementation-plan.md Phase 17's
// "mv, move, rn, and rename are all reserved".
func TestMvMoveRnRenameAreReserved(t *testing.T) {
	for _, tok := range []string{"mv", "move", "rn", "rename"} {
		if !IsReserved(tok) {
			t.Errorf(`IsReserved(%q) = false, want true`, tok)
		}
		if !slices.Contains(Reserved(), tok) {
			t.Errorf("Reserved() missing %q", tok)
		}
	}
}

// main is reserved for profiles only: it names the namespace root there, but
// means nothing at the namespace or repository level.
func TestIsReservedProfile_MainOnlyAtTheProfileLevel(t *testing.T) {
	if !IsReservedProfile("main") {
		t.Fatal("main should be reserved as a profile name")
	}
	if IsReserved("main") {
		t.Fatal("main should stay legal as a namespace or repository name")
	}
	if !IsReservedProfile("enable") {
		t.Fatal("ordinary reserved words stay reserved at the profile level")
	}
}
