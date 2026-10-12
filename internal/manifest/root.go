package manifest

import (
	"fmt"

	"github.com/DeprecatedLuar/ireallylovemydots/internal/grammar"
)

// CheckName enforces concept.md "Root namespace": name is required in the
// root .dots, forbidden in every other, and never a reserved word.
func CheckName(m Manifest, root bool) error {
	switch {
	case root && m.Name == "":
		return fmt.Errorf("the root .dots must set name = \"<namespace>\"")
	case !root && m.Name != "":
		return fmt.Errorf("name is only allowed in the repository root's .dots; a folder namespace is named by its folder")
	case root && grammar.IsReserved(m.Name):
		return fmt.Errorf("%q is a reserved word and cannot name a namespace", m.Name)
	}
	return nil
}
