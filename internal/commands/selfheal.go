package commands

import (
	"fmt"
	"os"

	"github.com/DeprecatedLuar/dotz/internal/selfheal"
	"github.com/DeprecatedLuar/dotz/internal/ui"
)

// RenderSelfHealFindings prints the two self-heal findings that print on
// every invocation regardless of command, per concept.md "Doctor": "Two
// findings are exempt, because there is no row to carry their mark and no
// `dots <ns>` to drill into: an unregistered clone in the data directory,
// and a data directory that holds no clones at all. Those print where they
// are found." Every other finding waits for `dots doctor` or `dots <ns>` —
// see HandleDoctor and selfheal.Findings.All/For.
func RenderSelfHealFindings(f selfheal.Findings) {
	if f.DataDirEmpty {
		fmt.Fprintln(os.Stderr, ui.WarningTone("! the data directory holds no repository clones, check XDG_DATA_HOME before continuing"))
		return
	}
	for _, name := range f.Unregistered {
		fmt.Fprintln(os.Stderr, ui.WarningTone(fmt.Sprintf("! %s  unregistered clone in the data directory, run: dots repo adopt %s", name, name)))
	}
}
