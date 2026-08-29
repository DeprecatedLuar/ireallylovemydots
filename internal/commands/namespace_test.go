package commands

import (
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
)

func TestHandleNamespace_AddRejectsReservedName(t *testing.T) {
	err := HandleNamespace([]string{"add", "list"}, shared.Flags{})
	if err == nil {
		t.Fatal("expected error creating a namespace named \"list\"")
	}
}

func TestHandleNamespace_MvRejectsReservedTarget(t *testing.T) {
	err := HandleNamespace([]string{"mv", "neovim", "sync"}, shared.Flags{})
	if err == nil {
		t.Fatal("expected error renaming a namespace to a reserved name")
	}
}
