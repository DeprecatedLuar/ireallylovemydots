package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/BurntSushi/toml"

	"github.com/DeprecatedLuar/dotz/internal/paths"
)

const (
	registryFileName      = "repositories.toml"
	localRegistryFileName = "local-repositories.toml"
)

// Origin marks which file a Repo was read from, and which file it is
// written back to. Never persisted itself — it is the tag ReadRegistry
// attaches to each entry, per concept.md "Repository manifest": "Which file
// an entry came from is remembered, so repo rm writes back to the right
// one."
type Origin int

const (
	// OriginConfig: the shared, committable config-directory registry.
	// Remote-backed repositories (repo add) live here.
	OriginConfig Origin = iota
	// OriginLocal: the machine-local state-directory registry. A repo init
	// repository has no remote and no meaning on another machine, so it is
	// never written to the shared file.
	OriginLocal
)

// Repo is one registered repository.
type Repo struct {
	Name   string `toml:"name"`
	Owner  string `toml:"owner"`
	URL    string `toml:"url"`
	Origin Origin `toml:"-"`
}

// Registry is the full set of registered repositories, config-registered
// and local combined.
type Registry struct {
	Repos []Repo `toml:"repos"`
}

// RegistryPath returns the shared repository manifest's path in the config
// directory.
func RegistryPath() (string, error) {
	configDir, err := paths.Config()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, registryFileName), nil
}

// LocalRegistryPath returns the local repository manifest's path in the
// state directory — the same shape as RegistryPath, for repositories
// registered by repo init that have no remote to be shared across machines.
func LocalRegistryPath() (string, error) {
	stateDir, err := paths.State()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, localRegistryFileName), nil
}

// ReadRegistry loads both the shared and the local repository manifest and
// returns their union, each entry tagged with the file it came from. A
// missing file is not an error: it contributes no entries.
func ReadRegistry() (Registry, error) {
	configPath, err := RegistryPath()
	if err != nil {
		return Registry{}, err
	}
	configRepos, err := readRegistryFile(configPath)
	if err != nil {
		return Registry{}, err
	}
	for i := range configRepos {
		configRepos[i].Origin = OriginConfig
	}

	localPath, err := LocalRegistryPath()
	if err != nil {
		return Registry{}, err
	}
	localRepos, err := readRegistryFile(localPath)
	if err != nil {
		return Registry{}, err
	}
	for i := range localRepos {
		localRepos[i].Origin = OriginLocal
	}

	return Registry{Repos: append(configRepos, localRepos...)}, nil
}

// WriteRegistry persists the repository manifest: every entry is written
// back to the file its Origin names, each sorted by name independently, per
// concept.md "repo rm writes back to the right one." An entry with the zero
// Origin value is treated as OriginConfig, so callers that build a Registry
// directly (never having gone through ReadRegistry) keep writing to the
// shared file, as before this split.
func WriteRegistry(r Registry) error {
	var configRepos, localRepos []Repo
	for _, repo := range r.Repos {
		if repo.Origin == OriginLocal {
			localRepos = append(localRepos, repo)
		} else {
			configRepos = append(configRepos, repo)
		}
	}

	configPath, err := RegistryPath()
	if err != nil {
		return err
	}
	if err := writeRegistryFile(configPath, configRepos); err != nil {
		return err
	}

	localPath, err := LocalRegistryPath()
	if err != nil {
		return err
	}
	return writeRegistryFile(localPath, localRepos)
}

func readRegistryFile(path string) ([]Repo, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read registry %s: %w", path, err)
	}

	var r Registry
	if err := toml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse registry %s: %w", path, err)
	}
	return r.Repos, nil
}

func writeRegistryFile(path string, repos []Repo) error {
	sorted := make([]Repo, len(repos))
	copy(sorted, repos)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	data, err := toml.Marshal(Registry{Repos: sorted})
	if err != nil {
		return fmt.Errorf("encode registry: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write registry %s: %w", path, err)
	}
	return nil
}
