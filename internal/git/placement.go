package git

// Source names which version of a top-level entry sync places somewhere.
type Source int

const (
	// SourceRemote is the remote's version, or HEAD's when there is no
	// remote branch to sync with.
	SourceRemote Source = iota
	// SourceLocal is this machine's working-tree version.
	SourceLocal
	// SourceMerged is the in-memory merge of both.
	SourceMerged
	// SourceUntouched leaves the working tree as it is; never valid for a
	// commit.
	SourceUntouched
)

// Placement is where one top-level entry's versions go: into the commit
// sync pushes, and into the working tree.
type Placement struct {
	Commit   Source
	Worktree Source
}
