package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/DeprecatedLuar/dotz/internal/paths"
)

const registryFileName = "repositories.json"

// Repo is one registered repository.
type Repo struct {
	Name  string `json:"name"`
	Owner string `json:"owner"`
	URL   string `json:"url"`
}

// Registry is the full set of registered repositories.
type Registry struct {
	Repos []Repo `json:"repos"`
}

// RegistryPath returns the repository manifest's path in the config directory.
func RegistryPath() (string, error) {
	configDir, err := paths.Config()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, registryFileName), nil
}

// ReadRegistry loads the repository manifest. A missing file is not an
// error: it returns an empty registry, the state before any repo is added.
func ReadRegistry() (Registry, error) {
	path, err := RegistryPath()
	if err != nil {
		return Registry{}, err
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Registry{}, nil
	}
	if err != nil {
		return Registry{}, fmt.Errorf("read registry %s: %w", path, err)
	}

	var r Registry
	if err := json.Unmarshal(data, &r); err != nil {
		return Registry{}, fmt.Errorf("parse registry %s: %w", path, err)
	}
	return r, nil
}

// WriteRegistry persists the repository manifest, sorted by name.
func WriteRegistry(r Registry) error {
	path, err := RegistryPath()
	if err != nil {
		return err
	}

	sorted := make([]Repo, len(r.Repos))
	copy(sorted, r.Repos)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	data, err := json.MarshalIndent(Registry{Repos: sorted}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode registry: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write registry %s: %w", path, err)
	}
	return nil
}
