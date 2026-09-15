package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/engine"
	"github.com/DeprecatedLuar/dotz/internal/fscopy"
	"github.com/DeprecatedLuar/dotz/internal/grammar"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/paths"
	"github.com/DeprecatedLuar/dotz/internal/state"
	"github.com/DeprecatedLuar/dotz/internal/ui"
)

// HandleMv implements `dots mv <src> <dst>`, aliased `move`: moves a whole
// namespace — its manifest, its .profiles/ directory, and every tracked
// payload — from one registered repository into another, per
// implementation-plan.md Phase 17. Unlike `cp`, it deletes the source, so
// both the source and the destination repository must be writable: cp's
// straight-out-of-git read path exists precisely so a read-only source can
// still be copied from, but mv has nothing to fall back to once the source
// is gone, so an unmaterialized-but-readable read-only source is refused
// the same as any other.
func HandleMv(args []string, flags shared.Flags) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: dots mv <repo/namespace> <repo/namespace> (or owner/repo/namespace)")
	}

	reg, err := manifest.ReadRegistry()
	if err != nil {
		return err
	}
	dataDir, err := paths.Data()
	if err != nil {
		return err
	}

	srcRepo, srcName, err := parseNamespaceSpec(reg.Repos, args[0])
	if err != nil {
		return err
	}
	dstRepo, dstName, err := parseNamespaceSpec(reg.Repos, args[1])
	if err != nil {
		return err
	}

	if grammar.IsReserved(dstName) {
		return fmt.Errorf("%q is a reserved word and cannot be used as a namespace name", dstName)
	}

	access, err := state.ReadAccess()
	if err != nil {
		return err
	}
	if access.IsReadOnly(srcRepo.Name) {
		return fmt.Errorf("repository %q is read-only on this machine; mv cannot remove a namespace from it", srcRepo.Name)
	}
	if access.IsReadOnly(dstRepo.Name) {
		return fmt.Errorf("repository %q is read-only on this machine; mv cannot write into it", dstRepo.Name)
	}

	srcDir := filepath.Join(dataDir, srcRepo.Name, srcName)
	dstDir := filepath.Join(dataDir, dstRepo.Name, dstName)

	if _, err := os.Stat(dstDir); err == nil {
		return fmt.Errorf("namespace %q already exists in repository %q; give a different destination name", dstName, dstRepo.Name)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", dstDir, err)
	}

	srcRepoDir := filepath.Join(dataDir, srcRepo.Name)
	installed, err := sourceInstalled(srcRepo, srcName, srcDir, srcRepoDir)
	if err != nil {
		return err
	}

	s, err := state.Read()
	if err != nil {
		return err
	}
	srcKey := state.Key{Repo: srcRepo.Name, Namespace: srcName}
	srcEnabled := s.Entries[srcKey].Enabled

	if installed {
		if err := fscopy.Copy(srcDir, dstDir); err != nil {
			return err
		}
	} else if err := copyNamespaceFromGit(srcRepoDir, srcName, dstDir); err != nil {
		return err
	}

	m, err := manifest.Read(dstDir)
	if err != nil {
		return err
	}
	m.Ignore = false
	if err := manifest.Write(dstDir, m); err != nil {
		return err
	}

	if srcEnabled {
		if err := moveEnabledNamespace(srcKey, srcRepo, srcName, srcDir, dstRepo, dstName, dstDir, m.Entries, s); err != nil {
			os.RemoveAll(dstDir)
			return err
		}
	} else if err := os.RemoveAll(srcDir); err != nil {
		return fmt.Errorf("remove source namespace %s: %w", srcDir, err)
	} else if err := removeSourceState(srcKey); err != nil {
		return err
	}

	fmt.Print(ui.Operation(ui.MarkerEnabled, dstName, dstRepo.Name))
	return nil
}

// moveEnabledNamespace handles the enabled-source handoff of Phase 17's
// step 6: pre-flight the destination against the copy just made — the only
// problem it may legitimately report is the source's own claim on every
// destination, since the source is still enabled at this point — then
// disable the source, enable the destination carrying the source's active
// profile forward, and finally remove the source namespace directory and
// its state entry. Nothing before the final removal is undone on failure,
// per the plan: a destination pre-flight failure is reported with the
// source left enabled and intact (the caller removes the partial copy); a
// failure disabling or enabling leaves whatever it already changed in
// place rather than guessing at a rollback.
func moveEnabledNamespace(srcKey state.Key, srcRepo manifest.Repo, srcName, srcDir string, dstRepo manifest.Repo, dstName, dstDir string, entries []manifest.Entry, s state.State) error {
	dstKey := state.Key{Repo: dstRepo.Name, Namespace: dstName}

	problems, err := engine.Preflight(dstKey, dstDir, entries, s)
	if err != nil {
		return err
	}
	for _, p := range problems {
		if p.Kind == engine.Collision && p.Conflicting != nil && *p.Conflicting == srcKey {
			continue
		}
		return fmt.Errorf("cannot move %q into repository %q: %s", dstName, dstRepo.Name, p.Message)
	}

	srcActiveProfile := s.Entries[srcKey].ActiveProfile

	if err := engine.Disable(srcKey, s); err != nil {
		return fmt.Errorf("disable source namespace %q before moving it: %w", srcName, err)
	}

	dstRepoDir := filepath.Dir(dstDir)
	s.Entries[dstKey] = state.Entry{ActiveProfile: srcActiveProfile}
	if _, err := engine.Enable(dstKey, dstRepoDir, dstDir, dstName, entries, s, nil); err != nil {
		return fmt.Errorf("enable destination namespace %q: %w", dstName, err)
	}

	if err := os.RemoveAll(srcDir); err != nil {
		return fmt.Errorf("remove source namespace %s: %w", srcDir, err)
	}
	return removeSourceState(srcKey)
}

// removeSourceState clears the source namespace's machine-state entry
// entirely, rather than leaving a disabled one behind — unlike an ordinary
// disable, mv's source namespace no longer exists to re-enable.
func removeSourceState(key state.Key) error {
	s, err := state.Read()
	if err != nil {
		return err
	}
	if _, ok := s.Entries[key]; !ok {
		return nil
	}
	delete(s.Entries, key)
	return state.Write(s)
}
