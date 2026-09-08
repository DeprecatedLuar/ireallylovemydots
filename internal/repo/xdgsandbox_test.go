package repo

import (
	"os"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/xdgtest"
)

// Every test in this package resolves dots's directories from the
// environment; xdgtest.Main confines all three to a sandbox so a test that
// overrides only some of them cannot reach the real ones.
func TestMain(m *testing.M) { os.Exit(xdgtest.Main(m)) }
