package commands

import (
	"fmt"

	"github.com/DeprecatedLuar/dotz/internal/ui"
)

// HandleList implements the top-level list, and its status alias. Its
// scope is every namespace in every registered repository, materialized or
// not. Until phase 3 registers a namespace catalogue, there is nothing to
// list, so it renders nothing — which is also what a successful run with
// nothing to report looks like.
func HandleList(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: list")
	}
	fmt.Print(ui.Render(nil))
	return nil
}
