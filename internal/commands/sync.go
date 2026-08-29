package commands

import (
	"fmt"

	"github.com/DeprecatedLuar/dotz/internal/manifest"
)

// HandleSync implements dots sync. The reconciliation engine is extracted
// from dredge in phase 9; with no repositories registered yet there is
// nothing to reconcile, so this succeeds silently.
func HandleSync(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: sync")
	}
	reg, err := manifest.ReadRegistry()
	if err != nil {
		return err
	}
	if len(reg.Repos) == 0 {
		return nil
	}
	return errNotImplemented("sync")
}
