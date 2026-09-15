package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DeprecatedLuar/dotz/internal/paths"
)

const accessFileName = "access.json"

// Access records per-repository push capability for this machine only. A
// repository absent from ReadOnly is treated as pushable — the map only
// ever names the exceptions.
type Access struct {
	ReadOnly map[string]bool // repository local name -> cannot push
}

// AccessPath returns the access file's path in the state directory.
func AccessPath() (string, error) {
	stateDir, err := paths.State()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, accessFileName), nil
}

// ReadAccess loads the access file. A missing file is not an error: it
// returns empty access, meaning every repository is pushable.
func ReadAccess() (Access, error) {
	path, err := AccessPath()
	if err != nil {
		return Access{}, err
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Access{ReadOnly: map[string]bool{}}, nil
	}
	if err != nil {
		return Access{}, fmt.Errorf("read access %s: %w", path, err)
	}

	var readOnly map[string]bool
	if err := json.Unmarshal(data, &readOnly); err != nil {
		return Access{}, fmt.Errorf("parse access %s: %w", path, err)
	}
	if readOnly == nil {
		readOnly = map[string]bool{}
	}
	return Access{ReadOnly: readOnly}, nil
}

// WriteAccess persists the access file as a flat JSON object mapping
// repository name to bool.
func WriteAccess(a Access) error {
	path, err := AccessPath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(a.ReadOnly, "", "  ")
	if err != nil {
		return fmt.Errorf("encode access: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write access %s: %w", path, err)
	}
	return nil
}

// IsReadOnly reports whether repoName is recorded as unable to push on this
// machine. A repository absent from the map is treated as pushable.
func (a Access) IsReadOnly(repoName string) bool {
	return a.ReadOnly[repoName]
}

// SetReadOnly records repoName's push capability for this machine.
func (a *Access) SetReadOnly(repoName string, ro bool) {
	if a.ReadOnly == nil {
		a.ReadOnly = map[string]bool{}
	}
	a.ReadOnly[repoName] = ro
}
