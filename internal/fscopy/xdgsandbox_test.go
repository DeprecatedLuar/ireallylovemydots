package fscopy

import (
	"os"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/xdgtest"
)

// fscopy's tests never touch XDG directories, but every test package is
// required to wire this so a future test that does add XDG-dependent
// coverage cannot silently reach the real machine's directories.
func TestMain(m *testing.M) { os.Exit(xdgtest.Main(m)) }
