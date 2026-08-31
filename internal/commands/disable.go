package commands

import (
	"fmt"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/engine"
	"github.com/DeprecatedLuar/dotz/internal/state"
	"github.com/DeprecatedLuar/dotz/internal/ui"
)

// disableNamespace implements `namespace <ns> disable` / `namespace disable
// <ns>` / `disable <ns>`, per concept.md "disable is not destructive":
// files stay on disk and re-enabling is instant. A namespace that was never
// enabled here is not an error to disable again. Per concept.md "What
// enable reports", disable prints "-" — the namespace's resulting state —
// through the same shared renderer every other mutation uses.
func disableNamespace(name string, flags shared.Flags) error {
	loc, err := resolveNamespace(name, flags)
	if err != nil {
		return err
	}
	s, err := state.Read()
	if err != nil {
		return err
	}
	key := state.Key{Repo: loc.Repo.Name, Namespace: name}
	if err := engine.Disable(key, s); err != nil {
		return err
	}
	fmt.Print(ui.Report([]string{ui.Operation(ui.MarkerMaterialized, name, "")}, ""))
	return nil
}
