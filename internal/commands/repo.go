package commands

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/DeprecatedLuar/dotz/internal/commands/shared"
	"github.com/DeprecatedLuar/dotz/internal/grammar"
	"github.com/DeprecatedLuar/dotz/internal/manifest"
	"github.com/DeprecatedLuar/dotz/internal/paths"
	"github.com/DeprecatedLuar/dotz/internal/repo"
	"github.com/DeprecatedLuar/dotz/internal/ui"
)

// HandleRepo implements the repo subtree: bare listing, the noun-level
// verbs that operate on a repository by name in an argument (add, rm), and
// the per-repository verb reached by naming the repository first (list).
//
// `rm` is deferred to phase 6, where removal semantics land.
func HandleRepo(args []string, flags shared.Flags) error {
	if len(args) == 0 {
		return renderRepoList()
	}

	if grammar.IsVerb(args[0]) {
		return handleRepoNounVerb(grammar.Canonical(args[0]), args[1:])
	}

	name := args[0]
	rest := args[1:]
	if len(rest) == 0 {
		return fmt.Errorf("usage: repo %s list", name)
	}
	if grammar.Canonical(rest[0]) != "list" {
		return fmt.Errorf("unknown verb %q for repo %q", rest[0], name)
	}
	return renderRepoNamespaces(name)
}

func handleRepoNounVerb(verb string, args []string) error {
	switch verb {
	case "add":
		if len(args) != 1 {
			return fmt.Errorf("usage: repo add <url>")
		}
		return addRepo(args[0])
	case "rm":
		if len(args) != 1 {
			return fmt.Errorf("usage: repo rm <repo>")
		}
		return errNotImplemented("repo rm")
	case "list":
		return renderRepoList()
	default:
		return fmt.Errorf("repo %s: not valid without a repository name", verb)
	}
}

// addRepo implements `repo add <url>`: derive the local name and owner from
// the URL, resolve any collision with a reserved word or an already
// registered name, clone blobless with no checkout, and register the
// result. Nothing is written to the registry unless the clone succeeds.
func addRepo(url string) error {
	reg, err := manifest.ReadRegistry()
	if err != nil {
		return err
	}

	derivedName, owner := repo.DeriveNameOwner(url)
	name, err := resolveNewRepoName(reg, derivedName)
	if err != nil {
		return err
	}

	dataDir, err := paths.Data()
	if err != nil {
		return err
	}
	_, resolvedURL, err := repo.Clone(dataDir, url, name)
	if err != nil {
		return err
	}

	reg.Repos = append(reg.Repos, manifest.Repo{Name: name, Owner: owner, URL: resolvedURL})
	if err := manifest.WriteRegistry(reg); err != nil {
		return err
	}

	fmt.Print(ui.Render([]ui.Entry{{Marker: ui.MarkerMaterialized, Name: name}}))
	return nil
}

// resolveNewRepoName returns a name safe to register: not a reserved word,
// not already taken. It prompts for an alternative when interactive and
// errors naming the conflict otherwise.
func resolveNewRepoName(reg manifest.Registry, name string) (string, error) {
	for {
		var reason string
		switch {
		case grammar.IsReserved(name):
			reason = fmt.Sprintf("%q is a reserved word and cannot be used as a repository name.", name)
		case repoNameTaken(reg, name):
			reason = fmt.Sprintf("%q is already registered.", name)
		default:
			return name, nil
		}

		if !ui.Interactive() {
			return "", fmt.Errorf("%s rerun with an explicit distinct name (non-interactive session)", reason)
		}
		resp, err := ui.Prompt(reason+" Choose a different local name:", nil)
		if err != nil {
			return "", err
		}
		resp = strings.TrimSpace(resp)
		if resp == "" {
			return "", fmt.Errorf("local name cannot be empty")
		}
		name = resp
	}
}

func repoNameTaken(reg manifest.Registry, name string) bool {
	for _, r := range reg.Repos {
		if strings.EqualFold(r.Name, name) {
			return true
		}
	}
	return false
}

func renderRepoList() error {
	reg, err := manifest.ReadRegistry()
	if err != nil {
		return err
	}
	entries := make([]ui.Entry, 0, len(reg.Repos))
	for _, r := range reg.Repos {
		entries = append(entries, ui.Entry{Marker: ui.MarkerMaterialized, Name: r.Name})
	}
	fmt.Print(ui.Render(entries))
	return nil
}

// renderRepoNamespaces lists one repository's namespace catalogue, reading
// straight from its cloned tree so unmaterialized namespaces still show up.
func renderRepoNamespaces(name string) error {
	reg, err := manifest.ReadRegistry()
	if err != nil {
		return err
	}
	r, err := repo.Resolve(reg.Repos, name)
	if err != nil {
		return err
	}

	dataDir, err := paths.Data()
	if err != nil {
		return err
	}
	names, err := repo.Namespaces(filepath.Join(dataDir, r.Name))
	if err != nil {
		return err
	}

	entries := make([]ui.Entry, 0, len(names))
	for _, n := range names {
		entries = append(entries, ui.Entry{Marker: ui.MarkerAbsent, Name: n})
	}
	fmt.Print(ui.Render(entries))
	return nil
}

// RepoNames returns every registered repository's local name, used by the
// router to resolve a bare top-level token against known repositories.
func RepoNames() ([]string, error) {
	reg, err := manifest.ReadRegistry()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(reg.Repos))
	for _, r := range reg.Repos {
		names = append(names, r.Name)
	}
	return names, nil
}
