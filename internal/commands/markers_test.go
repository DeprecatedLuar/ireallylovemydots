package commands

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
)

// Success criterion 1 (phase 8.9): enable, disable, install, uninstall, and
// rm each print the marker for the state the namespace ends in, per
// concept.md "What enable reports".

func TestEnableNamespace_PrintsPlusMarker(t *testing.T) {
	registerRepoWithNamespace(t, "editors", []manifest.Entry{{Name: "nvim", Dest: filepath.Join(t.TempDir(), "nvim")}})

	var err error
	stdout, _ := captureStdoutStderr(t, func() {
		err = enableNamespace("editors", shared.Flags{})
	})
	if err != nil {
		t.Fatalf("enableNamespace: %v", err)
	}
	if strings.TrimSpace(stdout) != "+ editors" {
		t.Fatalf("expected the resulting-state marker \"+\", got %q", stdout)
	}
}

func TestDisableNamespace_PrintsMinusMarker(t *testing.T) {
	registerRepoWithNamespace(t, "editors", []manifest.Entry{{Name: "nvim", Dest: filepath.Join(t.TempDir(), "nvim")}})
	if err := enableNamespace("editors", shared.Flags{}); err != nil {
		t.Fatalf("enableNamespace: %v", err)
	}

	var err error
	stdout, _ := captureStdoutStderr(t, func() {
		err = disableNamespace("editors", shared.Flags{})
	})
	if err != nil {
		t.Fatalf("disableNamespace: %v", err)
	}
	if strings.TrimSpace(stdout) != "- editors" {
		t.Fatalf("expected the resulting-state marker \"-\", got %q", stdout)
	}
}

func TestInstallNamespaces_PrintsMinusMarker(t *testing.T) {
	home := t.TempDir()
	source := newCatalogueSourceRepo(t, home)
	registerClonedCatalogue(t, source)

	var err error
	stdout, _ := captureStdoutStderr(t, func() {
		err = installNamespaces([]string{"editors"}, shared.Flags{})
	})
	if err != nil {
		t.Fatalf("installNamespaces: %v", err)
	}
	if strings.TrimSpace(stdout) != "- editors" {
		t.Fatalf("expected the resulting-state marker \"-\", got %q", stdout)
	}
}

func TestUninstallNamespaces_PrintsEqualsMarker(t *testing.T) {
	home := t.TempDir()
	source := newCatalogueSourceRepo(t, home)
	registerClonedCatalogue(t, source)
	if err := installNamespaces([]string{"editors"}, shared.Flags{}); err != nil {
		t.Fatalf("install editors: %v", err)
	}

	var err error
	stdout, _ := captureStdoutStderr(t, func() {
		err = uninstallNamespaces([]string{"editors"}, shared.Flags{Yes: true})
	})
	if err != nil {
		t.Fatalf("uninstallNamespaces: %v", err)
	}
	if strings.TrimSpace(stdout) != "= editors" {
		t.Fatalf("expected the resulting-state marker \"=\", got %q", stdout)
	}
}

func TestRmNamespaces_PrintsXMarker(t *testing.T) {
	registerRepoWithNamespace(t, "editors", []manifest.Entry{{Name: "nvim", Dest: filepath.Join(t.TempDir(), "nvim")}})

	var err error
	stdout, _ := captureStdoutStderr(t, func() {
		err = rmNamespaces([]string{"editors"}, shared.Flags{Yes: true})
	})
	if err != nil {
		t.Fatalf("rmNamespaces: %v", err)
	}
	if strings.TrimSpace(stdout) != "x editors" {
		t.Fatalf("expected the removal marker \"x\", got %q", stdout)
	}
}

// Success criterion 2: enabling one namespace over an occupied destination
// and enabling twenty-six (all occupied) produce the SAME SHAPE of report —
// the twenty-six case is twenty-six lines of the same per-namespace shape,
// not a different code path — and neither prompts.
func TestEnable_SingleAndBatchOfTwentySix_SameShapeNeverPrompts(t *testing.T) {
	home := registerRepoWithNamespaces(t, []string{"solo"}, nil)
	_ = home

	var soloErr error
	soloOut, _ := captureStdoutStderr(t, func() {
		soloErr = enableNamespaces([]string{"solo"}, shared.Flags{})
	})
	if !errors.Is(soloErr, ErrSomeSkipped) {
		t.Fatalf("expected the single occupied namespace to be skipped, got %v", soloErr)
	}
	soloLine := strings.TrimSpace(soloOut)
	if !strings.HasPrefix(soloLine, "! solo") {
		t.Fatalf("expected a single \"!\" report line, got %q", soloOut)
	}

	var names []string
	for i := 0; i < 26; i++ {
		names = append(names, "ns"+strconv.Itoa(i))
	}
	registerRepoWithNamespaces(t, names, nil)

	var batchErr error
	batchOut, _ := captureStdoutStderr(t, func() {
		batchErr = enableNamespaces(nil, shared.Flags{All: true})
	})
	if !errors.Is(batchErr, ErrSomeSkipped) {
		t.Fatalf("expected all twenty-six occupied namespaces to be skipped, got %v", batchErr)
	}
	lines := strings.Split(strings.TrimSpace(batchOut), "\n")
	if len(lines) != 26 {
		t.Fatalf("expected 26 report lines, got %d:\n%s", len(lines), batchOut)
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "! ") {
			t.Fatalf("expected every line to share the single namespace's \"!\" shape, got %q", l)
		}
	}
}

// Success criterion 3: a skip report for twenty-six namespaces is one line
// each, and each line names that namespace's destination.
func TestEnable_All_TwentySixOccupied_OneLineEachNamingDestination(t *testing.T) {
	var names []string
	for i := 0; i < 26; i++ {
		names = append(names, "ns"+strconv.Itoa(i))
	}
	home := registerRepoWithNamespaces(t, names, nil)

	var err error
	stdout, stderr := captureStdoutStderr(t, func() {
		err = enableNamespaces(nil, shared.Flags{All: true})
	})
	if !errors.Is(err, ErrSomeSkipped) {
		t.Fatalf("expected all namespaces to be skipped, got %v", err)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 26 {
		t.Fatalf("expected 26 lines, got %d:\n%s", len(lines), stdout)
	}
	for _, name := range names {
		dest := filepath.Join(home, name)
		found := false
		for _, l := range lines {
			if strings.Contains(l, dest) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected a line naming %s, got:\n%s", dest, stdout)
		}
	}
	if !strings.Contains(stderr, "26 skipped") {
		t.Fatalf("expected the stderr count line to name 26 skipped, got %q", stderr)
	}
}

// Success criterion 4: --force prints every trashed path as a sub-line
// under its namespace's operation line.
func TestEnable_Force_PrintsTrashedDestinationAsSubLine(t *testing.T) {
	home := registerRepoWithNamespaces(t, []string{"occA"}, nil)
	dest := filepath.Join(home, "occA")

	var err error
	stdout, _ := captureStdoutStderr(t, func() {
		err = enableNamespaces([]string{"occA"}, shared.Flags{Force: true})
	})
	if err != nil {
		t.Fatalf("expected --force to succeed, got %v", err)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected an operation line and one sub-line, got %d:\n%s", len(lines), stdout)
	}
	if strings.TrimSpace(lines[0]) != "+ occA" {
		t.Fatalf("expected the operation line \"+ occA\", got %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "  x ") || !strings.Contains(lines[1], dest) || !strings.Contains(lines[1], "trash") {
		t.Fatalf("expected an indented \"x\" sub-line naming the trashed destination, got %q", lines[1])
	}
}
