package commands

import (
	"fmt"

	"github.com/DeprecatedLuar/ireallylovemydots/internal/commands/shared"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/state"
	"github.com/DeprecatedLuar/ireallylovemydots/internal/syncmode"
)

// syncModeDefaultNote follows a mode shown because none is saved.
const syncModeDefaultNote = " (default)"

// handleSyncMode implements `namespace <ns> syncmode [<mode>]`: with no
// mode it prints the namespace's mode, otherwise it saves it in machine
// state; merge clears it back to the default.
func handleSyncMode(name string, args []string, flags shared.Flags) error {
	if len(args) > 1 {
		return fmt.Errorf("usage: namespace %s syncmode [<mode>]", name)
	}
	loc, err := resolveNamespace(name, flags)
	if err != nil {
		return err
	}
	if !loc.Installed {
		return errNotInstalled(name)
	}
	access, err := state.ReadAccess()
	if err != nil {
		return err
	}
	readOnly := access.IsReadOnly(loc.Repo.Name)
	s, err := state.Read()
	if err != nil {
		return err
	}
	key := state.Key{Repo: loc.Repo.Name, Namespace: name}
	entry := s.Entries[key]

	if len(args) == 0 {
		if entry.SyncMode == "" {
			fmt.Printf("%s: %s%s\n", name, syncmode.Default(readOnly), syncModeDefaultNote)
			return nil
		}
		fmt.Printf("%s: %s\n", name, entry.SyncMode)
		return nil
	}

	mode, err := syncmode.Parse(args[0])
	if err != nil {
		return err
	}
	if mode == syncmode.OverwriteRemote && readOnly {
		return fmt.Errorf("%q: %w", loc.Repo.Name, syncmode.ErrReadOnlyOverwriteRemote)
	}
	entry.SyncMode = string(mode)
	if mode == syncmode.Merge {
		entry.SyncMode = ""
	}
	s.Entries[key] = entry
	if err := state.Write(s); err != nil {
		return err
	}
	fmt.Printf("%s: %s\n", name, mode)
	return nil
}
