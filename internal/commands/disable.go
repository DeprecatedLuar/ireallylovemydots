package commands

import (
	"fmt"

	"github.com/DeprecatedLuar/ireallylovemydots/internal/commands/shared"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/engine"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/state"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/ui"
)

// alreadyDisabledDetail marks a namespace that was not enabled before the run.
const alreadyDisabledDetail = "already disabled"

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
	name = loc.Name
	s, err := state.Read()
	if err != nil {
		return err
	}
	key := state.Key{Repo: loc.Repo.Name, Namespace: name}
	detail := ""
	if !s.Entries[key].Enabled {
		detail = alreadyDisabledDetail
	}
	if err := engine.Disable(key, s); err != nil {
		return err
	}
	fmt.Print(ui.Report([]string{ui.Operation(ui.MarkerMaterialized, name, detail)}, ""))
	return nil
}
