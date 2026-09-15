package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/fscopy"
	"github.com/DeprecatedLuar/dotz/internal/git"
	"github.com/DeprecatedLuar/dotz/internal/grammar"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/paths"
	"github.com/DeprecatedLuar/dotz/internal/repo"
	"github.com/DeprecatedLuar/dotz/internal/state"
	"github.com/DeprecatedLuar/dotz/internal/ui"
)

// filePerm is the mode written for a file read out of git — git.ShowFile
// returns only content, never the tree entry's mode, so a namespace copied
// from a repository that was never materialized on this machine gets an
// ordinary file mode rather than whatever the source happened to carry.
const filePerm = 0644

// HandleCp implements `dots cp <src> <dst>`, aliased `copy`: copies a whole
// namespace directory — its manifest, its .profiles/ directory, and every
// tracked payload, filtering nothing out — from one registered repository
// into another. The source may be a read-only repository and need not be
// materialized on disk; it is read straight out of git in that case. The
// destination is never installed or enabled, and no provenance of the copy
// is recorded — cp only ever produces a fresh, ordinary namespace.
func HandleCp(args []string, flags shared.Flags) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: dots cp <repo/namespace> <repo/namespace> (or owner/repo/namespace)")
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
	if access.IsReadOnly(dstRepo.Name) {
		return fmt.Errorf("repository %q is read-only on this machine; cp cannot write into it", dstRepo.Name)
	}

	srcDir := filepath.Join(dataDir, srcRepo.Name, srcName)
	dstDir := filepath.Join(dataDir, dstRepo.Name, dstName)

	if _, err := os.Stat(dstDir); err == nil {
		return fmt.Errorf("namespace %q already exists in repository %q; give a different destination name", dstName, dstRepo.Name)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", dstDir, err)
	}

	installed, err := sourceInstalled(srcRepo, srcName, srcDir, filepath.Join(dataDir, srcRepo.Name))
	if err != nil {
		return err
	}

	if installed {
		if err := fscopy.Copy(srcDir, dstDir); err != nil {
			return err
		}
	} else if err := copyNamespaceFromGit(filepath.Join(dataDir, srcRepo.Name), srcName, dstDir); err != nil {
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

	fmt.Print(ui.Operation(ui.MarkerMaterialized, dstName, dstRepo.Name))
	return nil
}

// parseNamespaceSpec splits "repo/ns" or "owner/repo/ns" into a resolved
// repository and a namespace name: everything before the last "/" is the
// repository spec, resolved through repo.Resolve (which accepts both a bare
// local name and owner/name), and everything after is the namespace name. A
// single-segment spec, with no "/" at all, is an error naming the required
// form. Shared with dots mv (implementation-plan.md Phase 17).
func parseNamespaceSpec(repos []manifest.Repo, spec string) (manifest.Repo, string, error) {
	idx := strings.LastIndex(spec, "/")
	if idx < 0 {
		return manifest.Repo{}, "", fmt.Errorf("%q must be given as repo/namespace or owner/repo/namespace", spec)
	}

	repoSpec, nsName := spec[:idx], spec[idx+1:]
	if repoSpec == "" || nsName == "" {
		return manifest.Repo{}, "", fmt.Errorf("%q must be given as repo/namespace or owner/repo/namespace", spec)
	}

	r, err := repo.Resolve(repos, repoSpec)
	if err != nil {
		return manifest.Repo{}, "", err
	}
	return r, nsName, nil
}

// sourceInstalled reports whether the source namespace is materialized on
// disk, erroring if it does not exist at all or is filtered out by its
// repository's namespaces whitelist (Phase 15) — a filtered-out namespace is
// not copyable, whatever spec pointed at it.
func sourceInstalled(srcRepo manifest.Repo, srcName, srcDir, repoDir string) (bool, error) {
	if _, err := os.Stat(srcDir); err == nil {
		if !srcRepo.Allows(srcName, true) {
			return false, whitelistedOutError(srcName, srcRepo)
		}
		return true, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("stat %s: %w", srcDir, err)
	}

	catalogue, err := repo.Namespaces(repoDir)
	if err != nil {
		return false, err
	}
	if !slices.Contains(catalogue, srcName) {
		return false, fmt.Errorf("namespace %q not found in repository %q", srcName, srcRepo.Name)
	}
	if !srcRepo.Allows(srcName, false) {
		return false, whitelistedOutError(srcName, srcRepo)
	}
	return false, nil
}

// whitelistedOutError reports that name exists in r but is filtered out by
// its namespaces whitelist, mirroring internal/namespace's own
// whitelistError message (unexported there, so not directly reusable).
func whitelistedOutError(name string, r manifest.Repo) error {
	return fmt.Errorf("namespace %q exists in repository %q but is not in its namespaces whitelist", name, r.Name)
}

// copyNamespaceFromGit reads a namespace straight out of git, without
// materializing it in repoDir's own worktree first: every path committed
// under prefix at HEAD, written verbatim into dstDir. Used when the source
// namespace is not installed on this machine — most commonly a read-only
// repository's namespace, per implementation-plan.md Phase 16.
func copyNamespaceFromGit(repoDir, prefix, dstDir string) error {
	relPaths, err := git.ListTree(repoDir, "HEAD", prefix)
	if err != nil {
		return err
	}
	if len(relPaths) == 0 {
		return fmt.Errorf("namespace %q has no files committed in %s", prefix, repoDir)
	}

	for _, rel := range relPaths {
		srcPath := filepath.Join(prefix, rel)
		data, found, err := git.ShowFile(repoDir, "HEAD", srcPath)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("%s vanished from HEAD in %s while copying", srcPath, repoDir)
		}

		dst := filepath.Join(dstDir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return fmt.Errorf("create parent directory for %s: %w", dst, err)
		}
		if err := os.WriteFile(dst, data, filePerm); err != nil {
			return fmt.Errorf("write %s: %w", dst, err)
		}
	}
	return nil
}
