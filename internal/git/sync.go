package git

import (
	"fmt"
	"sort"
	"strings"
)

// Commit messages for the throwaway commits Prepare merges between; they
// never reach a branch.
const (
	baseCommitMsg     = "sync base"
	snapshotCommitMsg = "sync snapshot"
)

// scratchIdentity authors those throwaway commits, so syncing never needs
// the user's git identity unless it commits for real.
var scratchIdentity = []string{
	"GIT_AUTHOR_NAME=dots", "GIT_AUTHOR_EMAIL=dots@localhost",
	"GIT_COMMITTER_NAME=dots", "GIT_COMMITTER_EMAIL=dots@localhost",
}

// dotsFile marks a folder as a namespace of its own.
const dotsFile = ".dots"

// OwnerFunc names the unit that decides top-level entry name; hasDots
// reports whether it is a folder holding its own .dots.
type OwnerFunc func(name string, hasDots bool) string

// Unit is one top-level entry of a repository as Prepare found it.
type Unit struct {
	Name string
	Dir  bool
	Base string
	// Owner is the unit this entry decides with: itself, or the root namespace that owns it.
	Owner         string
	ChangedLocal  bool
	ChangedRemote bool
	// RemovedRemote is an entry the base has and the remote deleted.
	RemovedRemote bool
	Conflicts     []string
}

// Prepared is a repository fetched and merged in memory, nothing applied.
type Prepared struct {
	RemoteConfigured bool
	Head             string
	Remote           string
	Onto             string
	Units            []Unit
	local            string
	merged           string
}

// Prepare snapshots dir's working tree, fetches its branch, and merges the
// two in memory. Every entry is merged against its owner's base: heldBases'
// commit for an owner sync held earlier, else the merge base of HEAD and
// the remote. Nothing in dir changes except the remote-tracking ref.
func Prepare(dir string, heldBases map[string]string, owner OwnerFunc) (Prepared, error) {
	if isRebaseInProgress(dir) {
		if out, err := gitCmd(dir, "rebase", "--abort"); err != nil {
			return Prepared{}, fmt.Errorf("recover interrupted sync in %s: %s", dir, strings.TrimSpace(out))
		}
	}

	head, err := headCommit(dir)
	if err != nil {
		return Prepared{}, err
	}
	remoteConfigured, err := hasRemote(dir)
	if err != nil {
		return Prepared{}, err
	}
	p := Prepared{RemoteConfigured: remoteConfigured, Head: head}
	if remoteConfigured {
		if p.Remote, err = fetchRemoteHead(dir); err != nil {
			return Prepared{}, err
		}
	}
	p.Onto = p.Remote
	if p.Onto == "" {
		p.Onto = head
	}

	if p.local, err = snapshotTree(dir); err != nil {
		return Prepared{}, err
	}

	base := p.Onto
	if head != "" && p.Remote != "" {
		out, err := gitCmd(dir, "merge-base", head, p.Remote)
		if err != nil {
			return Prepared{}, fmt.Errorf("find merge base in %s: %s", dir, strings.TrimSpace(out))
		}
		base = strings.TrimSpace(out)
	}
	hasDots := map[string]bool{}
	if owner != nil {
		for _, tree := range []string{base, p.Onto, p.local} {
			folders, err := dotsFolders(dir, tree)
			if err != nil {
				return Prepared{}, err
			}
			for name := range folders {
				hasDots[name] = true
			}
		}
	}
	ownerOf := func(name string) string {
		if owner == nil {
			return name
		}
		return owner(name, hasDots[name])
	}
	baseEntries, unitBase, err := assembleBase(dir, base, heldBases, ownerOf)
	if err != nil {
		return Prepared{}, err
	}
	ontoEntries, err := topLevel(dir, p.Onto)
	if err != nil {
		return Prepared{}, err
	}
	localEntries, err := topLevel(dir, p.local)
	if err != nil {
		return Prepared{}, err
	}

	conflicts := map[string][]string{}
	p.merged = p.local
	if p.Onto != "" {
		var paths []string
		if p.merged, paths, err = mergeAgainst(dir, baseEntries, p.Onto, p.local); err != nil {
			return Prepared{}, err
		}
		for _, path := range paths {
			name, _, _ := strings.Cut(path, "/")
			conflicts[name] = append(conflicts[name], path)
		}
	}

	for _, name := range unionNames(baseEntries, ontoEntries, localEntries) {
		p.Units = append(p.Units, Unit{
			Name:          name,
			Dir:           isTreeLine(baseEntries[name]) || isTreeLine(ontoEntries[name]) || isTreeLine(localEntries[name]),
			Base:          unitBase(name),
			Owner:         ownerOf(name),
			ChangedLocal:  localEntries[name] != baseEntries[name],
			ChangedRemote: ontoEntries[name] != baseEntries[name],
			RemovedRemote: baseEntries[name] != "" && ontoEntries[name] == "",
			Conflicts:     conflicts[name],
		})
	}
	return p, nil
}

// fetchRemoteHead fetches dir's current branch and returns the remote
// head, or "" when the remote has no such branch yet.
func fetchRemoteHead(dir string) (string, error) {
	branch, err := getCurrentBranch(dir)
	if err != nil || branch == "" {
		return "", fmt.Errorf("sync requires an attached branch in %s", dir)
	}
	heads, err := gitCmd(dir, "ls-remote", "--heads", remoteName, "refs/heads/"+branch)
	if err != nil {
		return "", fmt.Errorf("inspect remote for %s: %s", dir, strings.TrimSpace(heads))
	}
	if strings.TrimSpace(heads) == "" {
		return "", nil
	}
	if err := fetch(dir, branch); err != nil {
		return "", err
	}
	return resolveRef(dir, "refs/remotes/"+remoteName+"/"+branch)
}

// assembleBase returns the top-level entries of the base every entry is
// merged against, and each entry's base commit: heldBases' commit for the
// entry's owner where one is recorded, base otherwise.
func assembleBase(dir, base string, heldBases map[string]string, ownerOf func(string) string) (map[string]string, func(string) string, error) {
	entries, err := topLevel(dir, base)
	if err != nil {
		return nil, nil, err
	}
	for owner, commit := range heldBases {
		held, err := topLevel(dir, commit)
		if err != nil {
			return nil, nil, err
		}
		for _, name := range unionNames(entries, held) {
			if ownerOf(name) != owner {
				continue
			}
			if line, ok := held[name]; ok {
				entries[name] = line
			} else {
				delete(entries, name)
			}
		}
	}
	unitBase := func(name string) string {
		if commit, ok := heldBases[ownerOf(name)]; ok {
			return commit
		}
		return base
	}
	return entries, unitBase, nil
}

// dotsFolders returns treeish's top-level folders that hold a .dots file.
// It reads tree objects only, so it works on a blobless clone.
func dotsFolders(dir, treeish string) (map[string]bool, error) {
	found := map[string]bool{}
	if treeish == "" {
		return found, nil
	}
	out, err := gitCmd(dir, "ls-tree", "-r", "--name-only", "-z", treeish)
	if err != nil {
		return nil, fmt.Errorf("list %s in %s: %s", treeish, dir, strings.TrimSpace(out))
	}
	for _, path := range strings.Split(out, "\x00") {
		folder, rest, nested := strings.Cut(path, "/")
		if nested && rest == dotsFile {
			found[folder] = true
		}
	}
	return found, nil
}

// mergeAgainst merges the local tree onto onto in memory, using a base
// commit built from baseEntries.
func mergeAgainst(dir string, baseEntries map[string]string, onto, localTree string) (string, []string, error) {
	baseTree, err := mkTree(dir, values(baseEntries))
	if err != nil {
		return "", nil, err
	}
	baseCommit, err := commitTreeEnv(dir, scratchIdentity, baseTree, baseCommitMsg)
	if err != nil {
		return "", nil, err
	}
	localCommit, err := commitTreeEnv(dir, scratchIdentity, localTree, snapshotCommitMsg)
	if err != nil {
		return "", nil, err
	}
	return mergeTree(dir, onto, localCommit, "--merge-base="+baseCommit)
}

// Finish applies placements: the working tree moves to its target in one
// read-tree, the commit tree is committed on top of p.Onto, and the index
// is reset to it. Entries placed SourceUntouched keep their files and read
// as local edits afterwards. commit false refuses any commit (a read-only
// repository). The caller must reapply the sparse-checkout cone before
// pushing: resetting the index drops skip-worktree bits. pushable reports
// a remote that lacks the new HEAD.
func Finish(dir string, p Prepared, placements map[string]Placement, commit bool) (bool, error) {
	if out, err := gitCmd(dir, "add", "-A"); err != nil {
		return false, fmt.Errorf("stage changes in %s: %s", dir, strings.TrimSpace(out))
	}
	out, err := gitCmd(dir, "write-tree")
	if err != nil {
		return false, fmt.Errorf("write index in %s: %s", dir, strings.TrimSpace(out))
	}
	from := strings.TrimSpace(out)

	sources := map[Source]string{SourceRemote: p.Onto, SourceLocal: p.local, SourceMerged: p.merged, SourceUntouched: from}
	entries := map[Source]map[string]string{}
	for s, treeish := range sources {
		if entries[s], err = topLevel(dir, treeish); err != nil {
			return false, err
		}
	}

	var worktree, final []string
	for _, u := range p.Units {
		pl, ok := placements[u.Name]
		if !ok {
			return false, fmt.Errorf("no sync placement for %s in %s", u.Name, dir)
		}
		if pl.Commit == SourceUntouched {
			return false, fmt.Errorf("%s in %s: an untouched working tree cannot be committed", u.Name, dir)
		}
		if line := entries[pl.Worktree][u.Name]; line != "" {
			worktree = append(worktree, line)
		}
		if line := entries[pl.Commit][u.Name]; line != "" {
			final = append(final, line)
		}
	}
	target, err := mkTree(dir, worktree)
	if err != nil {
		return false, err
	}
	finalTree, err := mkTree(dir, final)
	if err != nil {
		return false, err
	}

	if out, err := gitCmd(dir, "read-tree", "-m", "-u", from, target); err != nil {
		return false, fmt.Errorf("apply sync result in %s: %s", dir, strings.TrimSpace(out))
	}

	newHead, err := commitFinal(dir, p, finalTree, commit)
	if err != nil {
		return false, err
	}
	if newHead != "" && newHead != p.Head {
		if out, err := gitCmd(dir, "update-ref", "HEAD", newHead); err != nil {
			return false, fmt.Errorf("move branch in %s: %s", dir, strings.TrimSpace(out))
		}
	}
	if out, err := gitCmd(dir, "read-tree", finalTree); err != nil {
		return false, fmt.Errorf("reset index in %s: %s", dir, strings.TrimSpace(out))
	}
	return p.RemoteConfigured && newHead != "" && newHead != p.Remote, nil
}

// commitFinal returns the commit HEAD should point at: p.Onto when
// finalTree already matches it, else a new commit of finalTree on top.
func commitFinal(dir string, p Prepared, finalTree string, commit bool) (string, error) {
	ontoTree, err := treeOf(dir, p.Onto)
	if err != nil {
		return "", err
	}
	if finalTree == ontoTree {
		return p.Onto, nil
	}
	if !commit {
		return "", fmt.Errorf("sync in %s would commit local changes, which this repository does not allow", dir)
	}
	msg, err := commitMessage(dir, ontoTree, finalTree)
	if err != nil {
		return "", err
	}
	var parents []string
	if p.Onto != "" {
		parents = append(parents, p.Onto)
	}
	return commitTree(dir, finalTree, msg, parents...)
}

func unionNames(maps ...map[string]string) []string {
	seen := map[string]bool{}
	var names []string
	for _, m := range maps {
		for name := range m {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)
	return names
}

func values(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
