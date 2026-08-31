package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"strings"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/engine"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/paths"
	"github.com/DeprecatedLuar/dotz/internal/repo"
	"github.com/DeprecatedLuar/dotz/internal/state"
	"github.com/DeprecatedLuar/dotz/internal/ui"
)

// installNamespaces implements `install <ns>...` / `namespace install
// <ns>...`, per concept.md "Install and uninstall": puts each namespace's
// files on disk via sparse checkout, linking nothing. Already-installed
// namespaces are a no-op, not an error — install is idempotent, which is
// what lets `enable <ns>... -i` treat an already-installed member as the
// normal case rather than a failure partway through a batch.
func installNamespaces(names []string, flags shared.Flags) error {
	dataDir, err := paths.Data()
	if err != nil {
		return err
	}
	reg, err := manifest.ReadRegistry()
	if err != nil {
		return err
	}

	var report []ui.Entry
	for _, name := range names {
		_, repoDir, nsDir, err := locateNamespaceForEnable(dataDir, reg, name, flags)
		if err != nil {
			return err
		}
		if err := engine.Materialize(repoDir, nsDir, name); err != nil {
			return err
		}
		report = append(report, ui.Entry{Marker: ui.MarkerMaterialized, Name: name})
	}
	fmt.Print(ui.Render(report))
	return nil
}

// uninstallNamespaces implements `uninstall <ns>...` / `namespace uninstall
// <ns>...`, per concept.md "Install and uninstall": takes each namespace's
// files off disk via sparse checkout, keeping it tracked in the repository.
// Git safety (concept.md "Git safety on removal") is read once per
// repository before anything changes; --force overrides it. A namespace
// that is currently enabled prompts before disabling and uninstalling —
// -y answers the prompt, --force overrides it (implying -y per concept.md
// "Flags"), and a non-interactive session without either defaults to
// declining rather than erroring, per concept.md "Install and uninstall":
// "non-interactively the prompt defaults to no."
func uninstallNamespaces(names []string, flags shared.Flags) error {
	checkedRepos := map[string]bool{}
	var report []ui.Entry
	for _, name := range names {
		loc, err := resolveNamespace(name, flags)
		if err != nil {
			return err
		}
		repoDir := filepath.Dir(loc.Dir)

		if !checkedRepos[loc.Repo.Name] {
			if err := checkGitSafety(loc.Repo.Name, repoDir, flags); err != nil {
				return err
			}
			checkedRepos[loc.Repo.Name] = true
		}

		if _, err := os.Stat(loc.Dir); os.IsNotExist(err) {
			// Already uninstalled: nothing to do.
			continue
		}

		key := state.Key{Repo: loc.Repo.Name, Namespace: name}
		s, err := state.Read()
		if err != nil {
			return err
		}
		if s.Entries[key].Enabled {
			proceed, err := confirmUninstallEnabled(name, flags)
			if err != nil {
				return err
			}
			if !proceed {
				continue
			}
			if err := engine.Disable(key, s); err != nil {
				return err
			}
		}

		if err := repo.Remove(repoDir, name); err != nil {
			return err
		}
		report = append(report, ui.Entry{Marker: ui.MarkerAbsent, Name: name})
	}
	fmt.Print(ui.Render(report))
	return nil
}

// confirmUninstallEnabled asks whether to proceed uninstalling a currently
// enabled namespace. --force (which implies --yes) and -y both answer yes
// without asking; a non-interactive session with neither defaults to no,
// per concept.md "Install and uninstall".
func confirmUninstallEnabled(name string, flags shared.Flags) (bool, error) {
	if flags.Yes {
		return true, nil
	}
	if !ui.Interactive() {
		return false, nil
	}
	choice, err := ui.Prompt(fmt.Sprintf("%q is enabled; disable and uninstall it?", name), []string{"y", "N"})
	if err != nil {
		return false, err
	}
	return strings.EqualFold(choice, "y") || strings.EqualFold(choice, "yes"), nil
}
