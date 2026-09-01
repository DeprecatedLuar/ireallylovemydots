// Package repo holds git mechanics for the repository registry: URL
// parsing into a local name and owner, a blobless sparse clone so the
// namespace catalogue is readable without materializing file contents, and
// the sparse-checkout primitives (sparse.go) that bring a namespace's
// folder into or out of the working tree. No orchestration and no registry
// I/O — that is internal/commands/repo.go.
package repo

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DeprecatedLuar/dotz/internal/gitutil"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
)

// DeriveNameOwner parses a repository URL into a default local name (the
// URL's basename, with any ".git" suffix stripped) and its owner (the
// parent path segment), per concept.md "Name resolution". It handles both
// URL-style ("https://host/owner/name.git") and scp-style
// ("git@host:owner/name.git") remotes.
func DeriveNameOwner(url string) (name, owner string) {
	trimmed := strings.TrimSuffix(strings.TrimSuffix(url, "/"), ".git")

	if !strings.Contains(trimmed, "://") {
		if i := strings.Index(trimmed, ":"); i != -1 {
			trimmed = trimmed[:i] + "/" + trimmed[i+1:]
		}
	}

	parts := strings.Split(trimmed, "/")
	name = parts[len(parts)-1]
	if len(parts) >= 2 {
		owner = parts[len(parts)-2]
	}
	return name, owner
}

// Clone clones a repository blobless with an empty sparse-checkout cone
// into the data directory under localName, so the namespace catalogue can
// be read from the tree objects alone and no namespace folder is
// materialized until installed. spec may be a full URL (any scheme, or
// scp-style), a scheme-less host-qualified path, or a bare "owner/repo"
// GitHub/GitLab shorthand — see candidateURLs. The destination must not
// already exist; on failure of every candidate, the destination is removed,
// leaving no partial clone behind. Returns the destination path and the
// specific URL that actually succeeded, which is what callers should
// persist instead of spec.
func Clone(dataDir, spec, localName string) (dest, resolvedURL string, err error) {
	dest = filepath.Join(dataDir, localName)
	if _, statErr := os.Stat(dest); statErr == nil {
		return "", "", fmt.Errorf("clone destination %s already exists", dest)
	}

	candidates := candidateURLs(spec)
	var lastErr error
	for i, url := range candidates {
		cmd := exec.Command("git", "clone", "--filter=blob:none", "--sparse", url, dest)
		var tail gitutil.CappedWriter
		cmd.Stderr = io.MultiWriter(os.Stderr, &tail)
		cloneErr := cmd.Run()
		if cloneErr == nil {
			return dest, url, nil
		}
		os.RemoveAll(dest)
		out := tail.String()
		lastErr = fmt.Errorf("git clone %s: %s", url, strings.TrimSpace(out))

		isLastCandidate := i == len(candidates)-1
		if !isLastCandidate && looksLikeNotFound(out) {
			continue
		}
		return "", "", lastErr
	}
	return "", "", lastErr
}

// candidateURLs returns the ordered list of full URLs to attempt for a
// user-supplied repository spec, per concept.md "Repository level".
func candidateURLs(spec string) []string {
	if strings.Contains(spec, "://") || isSCPLike(spec) {
		return []string{spec}
	}

	owner, name, ok := strings.Cut(spec, "/")
	if !ok {
		return []string{spec}
	}
	if strings.Contains(owner, ".") {
		// Scheme-less but host-qualified, e.g. "github.com/owner/repo".
		return []string{"https://" + spec}
	}

	// Bare "owner/repo": GitHub shorthand, GitLab as fallback.
	return []string{
		fmt.Sprintf("https://github.com/%s/%s", owner, name),
		fmt.Sprintf("https://gitlab.com/%s/%s", owner, name),
	}
}

// isSCPLike reports whether spec is scp-style shorthand (user@host:path):
// a colon before any slash, with no "://" already ruled out by the caller.
func isSCPLike(spec string) bool {
	colon := strings.Index(spec, ":")
	if colon == -1 {
		return false
	}
	slash := strings.Index(spec, "/")
	return slash == -1 || colon < slash
}

// looksLikeNotFound reports whether git's clone output indicates the
// repository does not exist at that host, as opposed to a network or
// authentication failure — the only case worth trying the next candidate
// for instead of surfacing the error as-is.
func looksLikeNotFound(gitOutput string) bool {
	lower := strings.ToLower(gitOutput)
	for _, phrase := range []string{"not found", "does not exist", "could not be found"} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// Rename moves a repository's clone directory to newName within dataDir. It
// touches no registry entry and no state — those cross package boundaries
// this package does not otherwise touch; that is internal/commands/repo.go's
// job.
func Rename(dataDir, oldName, newName string) error {
	oldDir := filepath.Join(dataDir, oldName)
	newDir := filepath.Join(dataDir, newName)

	if _, err := os.Stat(oldDir); err != nil {
		return fmt.Errorf("repository %q not found in %s", oldName, dataDir)
	}
	if _, err := os.Stat(newDir); err == nil {
		return fmt.Errorf("repository %q already exists in %s", newName, dataDir)
	}
	if err := os.Rename(oldDir, newDir); err != nil {
		return fmt.Errorf("rename repository %s to %s: %w", oldDir, newDir, err)
	}
	return nil
}

// Resolve finds the repository named by spec against repos — a bare local
// name, or "owner/name" — case-insensitively, per concept.md "Name
// resolution".
func Resolve(repos []manifest.Repo, spec string) (manifest.Repo, error) {
	for _, r := range repos {
		if strings.EqualFold(r.Name, spec) {
			return r, nil
		}
	}
	for _, r := range repos {
		if strings.EqualFold(r.Owner+"/"+r.Name, spec) {
			return r, nil
		}
	}
	return manifest.Repo{}, fmt.Errorf("no registered repository matches %q", spec)
}
