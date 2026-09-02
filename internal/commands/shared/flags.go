// Package shared holds types passed between the router and every command
// package, so command implementations stay one file per command name.
package shared

// Flags carries the global flags extracted by the router from anywhere in
// os.Args. Command handlers read these instead of re-parsing argv.
type Flags struct {
	Repo      string
	All       bool
	Force     bool
	Purge     bool
	Yes       bool
	Debug     bool
	Bootstrap bool
	Install   bool
	Restore   bool
	// From is --from's value: the profile (or main) a new profile is seeded
	// from, per concept.md "Profile level". Empty means an empty profile.
	From string
}
