package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/DeprecatedLuar/dotz/internal/selfheal"
	"github.com/DeprecatedLuar/dotz/internal/ui"
)

// RenderSelfHealFindings prints self-heal's findings to stderr, per
// concept.md "Self-healing": self-heal itself never prints, so main.go
// hands its result here after every invocation. RenderSelfHealFindings
// covers what a listing cannot see on its own: an unregistered clone, a
// dropped state entry, a data directory that looks empty, a namespace
// manifest that still needs a human decision, or a namespace self-heal had
// to disable this pass because at least one destination was blocked — a
// listing shows the resulting "-" but not why, so that block names the
// destination and what occupies it. A manifest recovery that needed no
// further repair is not reported at all, per concept.md "Listing output":
// nothing prints on success.
//
// repairListCap caps how many broken namespaces the repair block names
// individually before folding the rest into a count, so many broken
// manifests do not bury the listing under this block.
const repairListCap = 10

func RenderSelfHealFindings(f selfheal.Findings) {
	if f.DataDirEmpty {
		fmt.Fprintln(os.Stderr, ui.WarningTone("! the data directory holds no repository clones, check XDG_DATA_HOME before continuing"))
		return
	}
	for _, name := range f.Unregistered {
		fmt.Fprintln(os.Stderr, ui.WarningTone(fmt.Sprintf("! %s  unregistered clone in the data directory, run: dots repo adopt %s", name, name)))
	}
	for _, key := range f.Dropped {
		fmt.Fprintln(os.Stderr, ui.Tip(fmt.Sprintf("dropped stale state for %s/%s, its repository no longer exists", key.Repo, key.Namespace)))
	}
	for _, r := range f.ConeRepaired {
		fmt.Fprintln(os.Stderr, ui.Tip(fmt.Sprintf("%s: %s", r.Repo, coneRepairDetail(r))))
	}
	renderDisabledBlock(f.Disabled)
	renderRepairBlock(f.NeedsRepair)
	renderProfileFindings(f.ProfileProblems, f.ProfileFallbacks)
}

// renderProfileFindings prints the .profiles/ reconciliation, per concept.md
// "Self-healing" under Profiles: the drift cases are warnings in the "?"
// tone, since both files involved are committed and neither side is at
// fault, while a fallback to main is reported with the destinations it
// relinked — it silently changes what sits at a destination, which is the
// only reason it is worth a line at all.
func renderProfileFindings(problems []selfheal.ProfileProblem, fallbacks []selfheal.ProfileFallback) {
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, ui.WarningTone(fmt.Sprintf("? %s  %s", profileSubject(p), p.Detail)))
	}
	for _, f := range fallbacks {
		fmt.Fprintln(os.Stderr, ui.WarningTone(fmt.Sprintf("! %s  profile %q no longer exists, fell back to main", f.Namespace, f.Profile)))
		for _, dest := range f.Relinked {
			fmt.Fprint(os.Stderr, ui.Sub(ui.MarkerEnabled, dest, "relinked"))
		}
	}
}

// profileSubject names what a profile problem is about, narrowing from the
// namespace down to the exact override where the case has one.
func profileSubject(p selfheal.ProfileProblem) string {
	parts := []string{p.Namespace}
	if p.Profile != "" {
		parts = append(parts, p.Profile)
	}
	if p.Entry != "" {
		parts = append(parts, p.Entry)
	}
	return strings.Join(parts, "/")
}

// coneRepairDetail phrases one repository's cone correction, per
// selfheal.ConeRepaired: a namespace added because it existed on disk
// outside the cone (uncommittable until now), a namespace dropped because
// its folder is gone (its absence now reads as "not installed here"
// rather than a deletion to commit), or both.
func coneRepairDetail(r selfheal.ConeRepaired) string {
	var parts []string
	if len(r.Added) > 0 {
		parts = append(parts, fmt.Sprintf("cone caught up with %s", strings.Join(r.Added, ", ")))
	}
	if len(r.Removed) > 0 {
		parts = append(parts, fmt.Sprintf("%s no longer installed here, dropped from the cone", strings.Join(r.Removed, ", ")))
	}
	return strings.Join(parts, "; ")
}

// renderDisabledBlock names every namespace Run auto-disabled this pass
// because at least one entry's destination was blocked, per concept.md
// "Self-healing": "enabled means every entry's symlink is correct and
// healthy — nothing less." Built from ui.Entry/RenderLines, the same
// renderer a listing uses, so a namespace named here carries the "-"
// marker and colour it now has in `dots ls`. The destination and what
// occupies it are folded into the entry's rendered name (RenderLines only
// concatenates marker and name, so this reuses the renderer rather than
// duplicating its colouring) in the same "<dest>    <detail>" column shape
// enable's problemSummary already uses.
func renderDisabledBlock(disabled []selfheal.Disabled) {
	if len(disabled) == 0 {
		return
	}

	// collapsed names every namespace whose reasons rendered as a count
	// rather than inline, so the tip below can point at `dots <ns>` for
	// exactly those — concept.md "Self-healing": "This report follows What
	// enable reports exactly."
	var collapsed []string
	entries := make([]ui.Entry, 0, min(len(disabled), repairListCap))
	for i, d := range disabled {
		if i == repairListCap {
			break
		}
		entries = append(entries, ui.Entry{Marker: ui.MarkerMaterialized, Name: d.Namespace + ui.DetailSep + disableReason(d.Reasons)})
		if len(d.Reasons) > 1 {
			collapsed = append(collapsed, d.Namespace)
		}
	}
	lines := ui.RenderLines(entries, os.Stderr)
	if len(disabled) > repairListCap {
		lines = append(lines, fmt.Sprintf("...and %d more", len(disabled)-repairListCap))
	}

	header := ui.WarningTone(fmt.Sprintf("%s disabled, their destination could not be linked:", ui.Plural(len(disabled), "namespace")))
	tip := ui.BlockedTip(collapsed, "run `dots enable <namespace>` to retry, add --force to trash the occupant")
	fmt.Fprint(os.Stderr, ui.List(header, lines, tip))
}

// disableReason reduces a disabled namespace's blocking problems to the one
// line concept.md "What enable reports" gives it — the same ui.BlockedSummary
// problemSummary in enable.go uses, so enable's and self-heal's reports
// describe the same occupied destination identically.
func disableReason(reasons []selfheal.Problem) string {
	blocked := make([]ui.Blocked, len(reasons))
	for i, p := range reasons {
		blocked[i] = ui.Blocked{Dest: p.Dest, Detail: p.Detail}
	}
	return ui.BlockedSummary(blocked)
}

// renderRepairBlock prints the one line the user asked for after "I had no
// idea what [the marker] meant": every namespace still needing a human
// decision, named up front, with the one command that fixes it, rather than
// leaving "!" to speak for itself. Built from ui.Entry/RenderLines, the same
// renderer a listing uses, so a namespace named here carries the identical
// marker and colour it would in `dots ls` rather than a second, uncoloured
// rendering of the same fact.
func renderRepairBlock(repairs []selfheal.Repair) {
	if len(repairs) == 0 {
		return
	}

	entries := make([]ui.Entry, 0, min(len(repairs), repairListCap))
	for i, r := range repairs {
		if i == repairListCap {
			break
		}
		entries = append(entries, ui.Entry{Marker: ui.MarkerProblem, Name: r.Namespace})
	}
	lines := ui.RenderLines(entries, os.Stderr)
	if len(repairs) > repairListCap {
		lines = append(lines, fmt.Sprintf("...and %d more", len(repairs)-repairListCap))
	}

	header := ui.WarningTone(fmt.Sprintf("%s need repairing:", ui.Plural(len(repairs), "namespace manifest")))
	fmt.Fprint(os.Stderr, ui.List(header, lines, "run `dots edit <namespace>` to amend one"))
}
