package commands

import (
	"fmt"
	"strings"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/engine"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/namespace"
	"github.com/DeprecatedLuar/dotz/internal/state"
	"github.com/DeprecatedLuar/dotz/internal/ui"
)

// restoreTarget is one namespace HandleRestore has already resolved and read
// the manifest for, carrying everything RestoreCopy needs alongside its own
// pre-flight problems.
type restoreTarget struct {
	name     string
	loc      namespace.Located
	key      state.Key
	entries  []manifest.Entry
	problems []engine.RestoreProblem
}

// HandleRestore implements `restore <ns>...` / `namespace restore <ns>...` /
// `namespace <ns> restore`, per implementation-plan.md's Phase 18: writes a
// real copy of every entry to its destination, replacing the namespace's
// symlinks. The namespace stays tracked, its payloads stay in the
// repository, and it ends disabled. Distinct from `rm --restore`, which
// moves payloads out and stops managing them entirely.
func HandleRestore(names []string, flags shared.Flags) error {
	targets := make([]restoreTarget, 0, len(names))
	for _, name := range names {
		loc, err := resolveNamespace(name, flags)
		if err != nil {
			return err
		}
		if !loc.Installed {
			return fmt.Errorf("namespace %q is not installed; run `dots install %s` first", name, name)
		}
		m, err := manifest.Read(loc.Dir)
		if err != nil {
			return err
		}
		problems, err := engine.RestorePreflight(loc.Dir, name, m.Entries)
		if err != nil {
			return err
		}
		targets = append(targets, restoreTarget{
			name:     name,
			loc:      loc,
			key:      state.Key{Repo: loc.Repo.Name, Namespace: name},
			entries:  m.Entries,
			problems: problems,
		})
	}

	skip, err := resolveRestoreOccupancy(targets, flags)
	if err != nil {
		return err
	}

	s, err := state.Read()
	if err != nil {
		return err
	}

	// lines is printed via defer so a mid-batch failure still reports every
	// namespace already restored before the error, rather than silently
	// dropping that partial progress on an early return.
	var lines []string
	defer func() { fmt.Print(ui.Report(lines, "")) }()
	for _, t := range targets {
		if err := engine.RestoreCopy(t.key, t.loc.Dir, t.entries, t.problems, s, skip); err != nil {
			return err
		}
		lines = append(lines, ui.Operation(ui.MarkerMaterialized, t.name, ""))
		for _, e := range t.entries {
			if e.HasDestination() {
				lines = append(lines, ui.Sub(ui.MarkerMaterialized, e.Dest, ""))
			}
		}
	}
	return nil
}

// resolveRestoreOccupancy resolves every occupied destination collected
// across the whole batch through one confirmation, mirroring rm.go's
// restoreEntries: --force resolves every occupied destination as [t] (trash
// the occupant), a non-interactive session without --force hard-errors, and
// interactively the choice is [t]rash / [s]kip / [c]ancel for the batch.
func resolveRestoreOccupancy(targets []restoreTarget, flags shared.Flags) (skip bool, err error) {
	var problems []engine.RestoreProblem
	total := 0
	for _, t := range targets {
		problems = append(problems, t.problems...)
		total += len(t.problems)
	}
	if total == 0 {
		return false, nil
	}

	if flags.Force {
		return false, nil
	}
	if !ui.Interactive() {
		return false, fmt.Errorf("cannot restore non-interactively: %d occupied destination(s):\n%s\nrerun with --force to trash the occupant(s) and restore, or answer interactively",
			total, renderRestoreProblems(problems))
	}
	choice, err := ui.Prompt(
		fmt.Sprintf("restoring has %d occupied destination(s):\n%s\n", total, renderRestoreProblems(problems)),
		"choose one",
		[]string{choiceTrash, choiceSkip, choiceCancel},
	)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case choiceTrash:
		return false, nil
	case choiceSkip:
		return true, nil
	default:
		return false, fmt.Errorf("restore cancelled")
	}
}
