package repo

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/DeprecatedLuar/dotz/internal/manifest"
)

// namespaceDirPerm is the mode Apply creates each new namespace folder with,
// matching the permissions namespace.Create uses elsewhere.
const namespaceDirPerm = 0755

// gitignorePerm is the mode RewriteGitignore writes the rewritten root
// .gitignore back with.
const gitignorePerm = 0644

// configDestPrefix is the "not dot-prefixed" half of concept.md "Bootstrap"'s
// destination rule: dot-prefixed goes to ~/<name>, everything else to
// ~/.config/<name>.
const configDestPrefix = ".config"

// bootstrapStagingSuffix names Apply's transient rename target, used only to
// move a root entry out of the way before its namespace folder of the same
// name is created underneath it.
const bootstrapStagingSuffix = ".dots-bootstrap-staging"

// skipExact is the closed set of root entries bootstrap never converts,
// per concept.md "Bootstrap": git and forge files.
var skipExact = map[string]bool{
	".git":           true,
	".gitignore":     true,
	".gitattributes": true,
	".gitmodules":    true,
	".github":        true,
	".gitlab-ci.yml": true,
}

// skipPrefixes matches root entries by their leading segment regardless of
// extension: README, LICENSE, and COPYING variants.
var skipPrefixes = []string{"README", "LICENSE", "COPYING"}

// skipGlobs matches loadout configuration files, which belong to dots
// itself rather than tracked content.
var skipGlobs = []string{"dotloadout.*.toml", "*loadout*.toml"}

// PlannedNamespace is one proposed conversion of a root entry into a
// namespace: the namespace it will become, the one entry it will hold
// (unchanged from the root entry's name), and that entry's destination.
type PlannedNamespace struct {
	Namespace string
	EntryName string
	Dest      string
}

// Plan builds the proposed --bootstrap conversion of repoPath's root
// entries, per concept.md "Bootstrap". It applies the skip set and the
// dot-prefixed -> ~/<name>, otherwise ~/.config/<name> destination rule, one
// namespace per root entry holding exactly one entry. Two entries resolving
// to the same namespace name is an error naming both. Plan reads only what
// RootEntries already returns, so preview and Apply can never disagree.
func Plan(repoPath string) ([]PlannedNamespace, error) {
	entries, err := RootEntries(repoPath)
	if err != nil {
		return nil, err
	}
	return PlanEntries(entries)
}

// PlanEntries is Plan's pure half, taking root entries directly rather than
// reading them from a git tree. repo init's compatibility check reads a
// folder that may not be a git repository yet (RootEntries requires one),
// via DiskEntries instead — this lets that same already-gathered entry list
// feed the bootstrap plan without a redundant, git-only re-read.
func PlanEntries(entries []RootEntry) ([]PlannedNamespace, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}

	var plan []PlannedNamespace
	sourceOf := map[string]string{}
	for _, e := range entries {
		if skipEntry(e) {
			continue
		}
		nsName, dest := destinationFor(home, e.Name, e.IsDir)
		if existing, dup := sourceOf[nsName]; dup {
			return nil, fmt.Errorf("%q and %q both resolve to namespace %q", existing, e.Name, nsName)
		}
		sourceOf[nsName] = e.Name
		plan = append(plan, PlannedNamespace{Namespace: nsName, EntryName: e.Name, Dest: dest})
	}

	sort.Slice(plan, func(i, j int) bool { return plan[i].Namespace < plan[j].Namespace })
	return plan, nil
}

// skipEntry reports whether a root entry is in bootstrap's closed skip set:
// already a namespace, or belonging to git, the forge, or dots itself.
func skipEntry(e RootEntry) bool {
	if e.HasDots {
		return true
	}
	if skipExact[e.Name] {
		return true
	}
	for _, prefix := range skipPrefixes {
		if strings.HasPrefix(e.Name, prefix) {
			return true
		}
	}
	for _, pattern := range skipGlobs {
		if matched, _ := filepath.Match(pattern, e.Name); matched {
			return true
		}
	}
	return false
}

// destinationFor applies concept.md "Bootstrap"'s one-line rule: dot-prefixed
// goes to ~/<name> (dot included), with the leading dot dropped from the
// namespace name; everything else goes to ~/.config/<name>. The destination
// always keeps the entry's name verbatim — only the namespace name is
// cleaned up, which is safe because a namespace records its entry's name
// and destination independently of what the namespace itself is called.
func destinationFor(home, name string, isDir bool) (nsName, dest string) {
	if strings.HasPrefix(name, ".") {
		dest = filepath.Join(home, name)
	} else {
		dest = filepath.Join(home, configDestPrefix, name)
	}
	return namespaceNameFor(name, isDir), dest
}

// namespaceNameFor turns a root entry's name into a namespace name: the
// leading dot dropped, then the file extension dropped so `starship.toml`
// becomes the namespace `starship` rather than `starship.toml`. Only files
// are stripped — a directory's extension is part of its name (`nvim.d` is
// not `nvim`). The dot must be dropped before the extension is read, or
// filepath.Ext would treat the whole of `.zshrc` as one extension. Two
// entries reduced to the same name are caught by PlanEntries, which errors
// naming both rather than merging them.
func namespaceNameFor(name string, isDir bool) string {
	nsName := strings.TrimPrefix(name, ".")
	if isDir {
		return nsName
	}
	if stripped := strings.TrimSuffix(nsName, filepath.Ext(nsName)); stripped != "" {
		return stripped
	}
	return nsName
}

// GitignoreOutcome classifies what happened to one line of a root
// .gitignore under bootstrap's rewrite, per concept.md "Bootstrap rewrites
// the root .gitignore".
type GitignoreOutcome int

const (
	// GitignoreRewritten: the pattern's first path segment names a
	// converted entry, so its namespace was prepended.
	GitignoreRewritten GitignoreOutcome = iota
	// GitignoreUnchanged: a slashless pattern (git matches it at any
	// depth regardless of restructuring), or a blank/comment line.
	GitignoreUnchanged
	// GitignoreUnmapped: the pattern's first segment names no converted
	// entry — a path from an older layout, left as-is and reported.
	GitignoreUnmapped
)

// GitignoreChange is what happened to one line of a root .gitignore under
// bootstrap's rewrite. Rewritten equals Original whenever Outcome is not
// GitignoreRewritten, so the slice alone reconstructs the file.
type GitignoreChange struct {
	Outcome   GitignoreOutcome
	Original  string
	Rewritten string
}

// PlanGitignoreLines classifies every line of a root .gitignore against
// plan's move map, per concept.md "Bootstrap rewrites the root .gitignore":
// "Bootstrap's transformation is total and one-way — every root entry X
// moves to <namespace>/X — so the move map answers every pattern that has
// a path in it." Blank lines and comments pass through untouched as
// GitignoreUnchanged.
func PlanGitignoreLines(lines []string, plan []PlannedNamespace) []GitignoreChange {
	moved := make(map[string]string, len(plan)) // original entry name -> namespace
	for _, p := range plan {
		moved[p.EntryName] = p.Namespace
	}

	changes := make([]GitignoreChange, len(lines))
	for i, line := range lines {
		changes[i] = planGitignoreLine(line, moved)
	}
	return changes
}

// planGitignoreLine classifies one .gitignore line per concept.md's table:
// a converted entry's first segment gets its namespace prepended
// (negation and a rooting leading slash carried through unchanged); a
// slashless pattern is left alone; anything else maps to nothing and is
// reported unmapped.
func planGitignoreLine(line string, moved map[string]string) GitignoreChange {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return GitignoreChange{Outcome: GitignoreUnchanged, Original: line, Rewritten: line}
	}

	negated := strings.HasPrefix(line, "!")
	body := line
	if negated {
		body = body[1:]
	}
	rooted := strings.HasPrefix(body, "/")
	if rooted {
		body = body[1:]
	}

	// Per gitignore(5): a pattern is rooted to this directory only if it has
	// a slash at the beginning (already captured above) or in the middle —
	// a slash that appears ONLY at the end (a trailing "build/") does not
	// root it and still matches at any depth, exactly like a slashless
	// pattern. So a slash found only as the last character doesn't count.
	withoutTrailing := strings.TrimSuffix(body, "/")
	hasMiddleSlash := strings.Contains(withoutTrailing, "/")
	if !rooted && !hasMiddleSlash {
		return GitignoreChange{Outcome: GitignoreUnchanged, Original: line, Rewritten: line}
	}

	firstSegment, _, _ := strings.Cut(body, "/")
	namespace, ok := moved[firstSegment]
	if !ok {
		return GitignoreChange{Outcome: GitignoreUnmapped, Original: line, Rewritten: line}
	}

	var rewritten strings.Builder
	if negated {
		rewritten.WriteByte('!')
	}
	if rooted {
		rewritten.WriteByte('/')
	}
	rewritten.WriteString(namespace)
	rewritten.WriteByte('/')
	rewritten.WriteString(body)
	return GitignoreChange{Outcome: GitignoreRewritten, Original: line, Rewritten: rewritten.String()}
}

// readGitignoreLines reads repoPath's root .gitignore, if present, split
// into lines with any trailing newline recorded separately so it can be
// reproduced exactly. A missing file returns a nil slice, not an error —
// not every bootstrapped repository has one.
func readGitignoreLines(repoPath string) (lines []string, trailingNewline bool, err error) {
	path := filepath.Join(repoPath, ".gitignore")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	trailingNewline = strings.HasSuffix(string(data), "\n")
	lines = strings.Split(string(data), "\n")
	if trailingNewline {
		lines = lines[:len(lines)-1]
	}
	return lines, trailingNewline, nil
}

// PlanGitignore reads repoPath's root .gitignore, if present, and
// classifies every pattern against plan's move map, without writing
// anything — the preview half of the rewrite, so a declined bootstrap never
// touches the file. A repository with no root .gitignore reports no
// changes.
func PlanGitignore(repoPath string, plan []PlannedNamespace) ([]GitignoreChange, error) {
	lines, _, err := readGitignoreLines(repoPath)
	if err != nil {
		return nil, err
	}
	if lines == nil {
		return nil, nil
	}
	return PlanGitignoreLines(lines, plan), nil
}

// RewriteGitignore rewrites repoPath's root .gitignore in place per plan's
// move map, per concept.md "Bootstrap rewrites the root .gitignore". A
// repository with no root .gitignore is left alone. Called by Apply, once
// the conversion the preview described has been confirmed.
func RewriteGitignore(repoPath string, plan []PlannedNamespace) ([]GitignoreChange, error) {
	lines, trailingNewline, err := readGitignoreLines(repoPath)
	if err != nil {
		return nil, err
	}
	if lines == nil {
		return nil, nil
	}

	changes := PlanGitignoreLines(lines, plan)
	rewritten := make([]string, len(changes))
	for i, c := range changes {
		rewritten[i] = c.Rewritten
	}
	out := strings.Join(rewritten, "\n")
	if trailingNewline {
		out += "\n"
	}
	path := filepath.Join(repoPath, ".gitignore")
	if err := os.WriteFile(path, []byte(out), gitignorePerm); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return changes, nil
}

// Apply performs the conversion Plan proposed: for each planned namespace,
// creates its folder, moves the root entry into it, writes its .dots, and
// rewrites the root .gitignore (per concept.md "Bootstrap rewrites the root
// .gitignore") so it still matches the paths it did before restructuring.
// Never commits, never creates a symlink, and never writes outside
// repoPath.
func Apply(repoPath string, plan []PlannedNamespace) error {
	for _, p := range plan {
		src := filepath.Join(repoPath, p.EntryName)
		nsDir := filepath.Join(repoPath, p.Namespace)

		// The namespace name equals the entry name for every non-dot-prefixed
		// entry, so nsDir and src are frequently the same path — moving src
		// aside before nsDir is created is what keeps a directory from being
		// renamed into its own soon-to-exist subdirectory.
		staged := src + bootstrapStagingSuffix
		if err := os.Rename(src, staged); err != nil {
			return fmt.Errorf("stage %s for namespace %s: %w", src, p.Namespace, err)
		}

		if err := os.MkdirAll(nsDir, namespaceDirPerm); err != nil {
			return fmt.Errorf("create namespace directory %s: %w", nsDir, err)
		}

		dst := filepath.Join(nsDir, p.EntryName)
		if err := moveEntry(staged, dst); err != nil {
			return fmt.Errorf("move %s into %s: %w", src, dst, err)
		}

		m := manifest.Manifest{Entries: []manifest.Entry{{Name: p.EntryName, Dest: p.Dest}}}
		if err := manifest.Write(nsDir, m); err != nil {
			return fmt.Errorf("write manifest for namespace %s: %w", p.Namespace, err)
		}
	}

	if _, err := RewriteGitignore(repoPath, plan); err != nil {
		return err
	}
	return nil
}

// moveEntry moves src to dst, falling back to copyTree (Take's own
// fallback) on a cross-device error — bootstrap moves entries within one
// repository, which ordinarily shares a filesystem, but must not assume it.
func moveEntry(src, dst string) error {
	renameErr := os.Rename(src, dst)
	if renameErr == nil {
		return nil
	}
	if !errors.Is(renameErr, syscall.EXDEV) {
		return renameErr
	}
	if err := copyTree(src, dst); err != nil {
		os.RemoveAll(dst)
		return err
	}
	return os.RemoveAll(src)
}

// CheckoutAll fetches every blob and checks out repoPath's HEAD in full,
// turning a blobless clone with an empty sparse cone into a complete
// working tree. This is bootstrap's one exception to repo add's sparse
// clone (concept.md "Bootstrap") — paid only once the user has confirmed
// the conversion. `git checkout HEAD -- .` alone would still be bound by
// the empty cone left by Clone's `--sparse` flag and materialize nothing
// outside it, so sparse checkout is switched off first; the caller
// re-establishes the correct cone afterward via EnsureSparse once the
// conversion's namespace folders are known.
func CheckoutAll(repoPath string) error {
	if err := disableSparse(repoPath); err != nil {
		return err
	}
	cmd := exec.Command("git", "-C", repoPath, "checkout", "HEAD", "--", ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("checkout %s: %s", repoPath, strings.TrimSpace(string(out)))
	}
	return nil
}
