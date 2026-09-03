package repo

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Init turns on cone-mode sparse checkout for the repository at repoDir,
// leaving it with an empty cone (root files only) — the starting point
// Clone's `--sparse` flag also produces. Callers add paths afterward via
// Add, or replace the whole cone in one step via EnsureSparse.
func Init(repoDir string) error {
	cmd := exec.Command("git", "-C", repoDir, "sparse-checkout", "init", "--cone")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sparse-checkout init %s: %s", repoDir, strings.TrimSpace(string(out)))
	}
	return nil
}

// Add brings names into the repository's sparse-checkout cone alongside
// whatever is already there — cone mode's "add" is additive, unlike "set"
// which replaces the whole cone. Git exits zero even when it silently
// refuses to materialize a path, so the result is verified against the
// filesystem rather than trusted from the exit status alone — List is not
// reliable for this: it reports the cone git intended to apply, not
// whether a path actually landed on disk (see Remove's doc comment for the
// empirical case that rules List out).
func Add(repoDir string, names ...string) error {
	if len(names) == 0 {
		return nil
	}
	args := append([]string{"-C", repoDir, "sparse-checkout", "add"}, names...)
	cmd := exec.Command("git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sparse-checkout add %s in %s: %s", strings.Join(names, ", "), repoDir, strings.TrimSpace(string(out)))
	}

	for _, n := range names {
		if _, err := os.Stat(filepath.Join(repoDir, n)); err != nil {
			return fmt.Errorf("sparse-checkout add %s in %s: %q missing from the working tree afterward", strings.Join(names, ", "), repoDir, n)
		}
	}
	return nil
}

// Remove drops names from the repository's sparse-checkout cone. Cone mode
// has no native per-path removal, so this reads the current cone, filters
// names out, and re-sets the remainder in one call.
//
// Verification is against the filesystem, not List: against real git
// 2.51.2, removing a namespace holding an uncommitted edit prints a
// warning ("left despite sparse patterns") but still exits zero, AND
// List() no longer reports the namespace afterward even though its
// directory (holding the dirty file) is still on disk — List reflects the
// cone git recorded, not what it actually achieved. A namespace directory
// still present after the attempted removal is reported as a refusal.
func Remove(repoDir string, names ...string) error {
	if len(names) == 0 {
		return nil
	}
	current, err := List(repoDir)
	if err != nil {
		return err
	}

	drop := stringSet(names)
	var remaining []string
	for _, c := range current {
		if !drop[c] {
			remaining = append(remaining, c)
		}
	}
	if err := setCone(repoDir, remaining); err != nil {
		return err
	}

	for _, n := range names {
		if _, err := os.Stat(filepath.Join(repoDir, n)); err == nil {
			return fmt.Errorf("sparse-checkout remove %s in %s: git refused (uncommitted changes?)", strings.Join(names, ", "), repoDir)
		}
	}
	return nil
}

// List returns the repository's current sparse-checkout cone: the
// top-level names currently included, per `git sparse-checkout list`.
func List(repoDir string) ([]string, error) {
	cmd := exec.Command("git", "-C", repoDir, "sparse-checkout", "list")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("sparse-checkout list %s: %s", repoDir, strings.TrimSpace(string(out)))
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil, nil
	}
	return strings.Split(trimmed, "\n"), nil
}

// Reapply re-applies the repository's existing cone to the working tree,
// per concept.md "Sparse checkout": sync runs this after rebasing, since
// merge and rebase can materialize paths outside the cone in order to do
// their work.
func Reapply(repoDir string) error {
	cmd := exec.Command("git", "-C", repoDir, "sparse-checkout", "reapply")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sparse-checkout reapply %s: %s", repoDir, strings.TrimSpace(string(out)))
	}
	return nil
}

// IsSparse reports whether cone-mode sparse checkout is currently enabled
// for the repository at repoDir, read from core.sparseCheckout directly
// rather than inferred from a file's existence or a command's exit status.
// An unset config value reads as false, not an error.
func IsSparse(repoDir string) (bool, error) {
	cmd := exec.Command("git", "-C", repoDir, "config", "--bool", "core.sparseCheckout")
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return false, nil
		}
		return false, fmt.Errorf("read core.sparseCheckout for %s: %w", repoDir, err)
	}
	return strings.TrimSpace(string(out)) == "true", nil
}

// EnsureSparse converts repoDir into cone-mode sparse checkout — turning it
// on first if it is not already — and sets its cone to exactly cone, no
// more and no less. This is how a repository created or cloned before
// sparse checkout existed (or created fully checked out, like `repo init`
// and bootstrap's on-disk conversion) is brought in line, per concept.md
// "Sparse checkout": "Repositories that are not sparse are converted, never
// left." The caller decides what belongs in the cone — conversion reads it
// from what is given and never invents or drops a path beyond that.
func EnsureSparse(repoDir string, cone []string) error {
	sparse, err := IsSparse(repoDir)
	if err != nil {
		return err
	}
	if !sparse {
		if err := Init(repoDir); err != nil {
			return err
		}
	}
	if err := setCone(repoDir, cone); err != nil {
		return err
	}

	for _, n := range cone {
		if _, err := os.Stat(filepath.Join(repoDir, n)); err != nil {
			return fmt.Errorf("sparse-checkout convert %s: %q missing from the working tree after set", repoDir, n)
		}
	}
	return nil
}

// ReconcileCone brings repoDir's sparse-checkout cone into line with what
// is actually materialized on disk, per concept.md "Sparse checkout":
// "everything present is installed by definition." Nothing that creates a
// namespace folder directly on the worktree — namespace.Create,
// namespace.Rename — extends the cone itself, so without this the cone and
// the working tree can permanently disagree in both directions:
//
//   - a top-level directory on disk but missing from the cone (created or
//     renamed straight on the worktree) can never be staged: git refuses
//     `add -A` outright for any path outside the cone.
//   - a name in the cone with no matching directory on disk (removed by
//     hand, outside dots) reads to git as a deletion — the exact failure
//     mode concept.md's "Sparse checkout" describes for an unconverted
//     repository, staging the removal of everything not installed here.
//
// Both directions are corrected the same non-destructive way EnsureSparse
// already guarantees for the one-time repo-init/bootstrap conversion: the
// desired cone is read from what is on disk, never invented, so adding a
// name has a directory to check out against and dropping one has nothing
// on disk to remove — EnsureSparse only ever changes the skip-worktree
// bit, never the working tree or the index.
//
// added and removed report what changed, sorted, for a caller to surface
// (self-heal's cone-repair finding) — both nil when the cone already
// matched disk.
func ReconcileCone(repoDir string) (added, removed []string, err error) {
	entries, err := DiskEntries(repoDir)
	if err != nil {
		return nil, nil, err
	}
	onDisk := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir && !strings.HasPrefix(e.Name, ".") {
			onDisk = append(onDisk, e.Name)
		}
	}
	sort.Strings(onDisk)

	sparse, err := IsSparse(repoDir)
	if err != nil {
		return nil, nil, err
	}
	var current []string
	if sparse {
		current, err = List(repoDir)
		if err != nil {
			return nil, nil, err
		}
	}

	onDiskSet := stringSet(onDisk)
	for _, name := range current {
		if !onDiskSet[name] {
			removed = append(removed, name)
		}
	}
	currentSet := stringSet(current)
	for _, name := range onDisk {
		if !currentSet[name] {
			added = append(added, name)
		}
	}
	if sparse && len(added) == 0 && len(removed) == 0 {
		return nil, nil, nil
	}
	sort.Strings(added)
	sort.Strings(removed)

	if err := EnsureSparse(repoDir, onDisk); err != nil {
		return nil, nil, err
	}
	return added, removed, nil
}

// setCone replaces the repository's entire sparse-checkout cone with
// names, per `git sparse-checkout set` — as opposed to Add's incremental
// append.
func setCone(repoDir string, names []string) error {
	args := append([]string{"-C", repoDir, "sparse-checkout", "set"}, names...)
	cmd := exec.Command("git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sparse-checkout set in %s: %s", repoDir, strings.TrimSpace(string(out)))
	}
	return nil
}

// disableSparse turns sparse checkout off entirely, materializing every
// path the cone previously excluded. It exists for CheckoutAll's benefit:
// `git checkout HEAD -- .` is itself bound by an active cone and would
// leave excluded paths untouched, so bootstrap's one-time full checkout
// needs sparse checkout out of the way first. Not exported — every other
// caller wants a cone, never no cone at all, so EnsureSparse (which
// re-enables cone mode) is the public entry point back into sparseness.
func disableSparse(repoDir string) error {
	cmd := exec.Command("git", "-C", repoDir, "sparse-checkout", "disable")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sparse-checkout disable %s: %s", repoDir, strings.TrimSpace(string(out)))
	}
	return nil
}

func stringSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}
