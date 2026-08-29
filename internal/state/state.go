// Package state records what is materialized on disk: the machine-local,
// never-shared counterpart to the manifests. Absence of an entry means the
// namespace has not been downloaded.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/DeprecatedLuar/dotz/internal/paths"
)

const fileName = "state.json"

// Key identifies a namespace by repository plus name, since two
// repositories may both ship a namespace with the same name.
type Key struct {
	Repo      string `json:"repo"`
	Namespace string `json:"namespace"`
}

// Entry is the recorded state of one namespace on this machine.
type Entry struct {
	Enabled       bool     `json:"enabled"`
	ActiveProfile string   `json:"activeProfile,omitempty"`
	LinkedDests   []string `json:"linkedDests,omitempty"`
}

// State is the full set of recorded namespace entries, keyed by repo plus
// namespace.
type State struct {
	Entries map[Key]Entry `json:"-"`
}

// record is the on-disk shape: a flat list, since a Go map can't be a JSON
// object key when the key itself is a struct.
type record struct {
	Repo          string   `json:"repo"`
	Namespace     string   `json:"namespace"`
	Enabled       bool     `json:"enabled"`
	ActiveProfile string   `json:"activeProfile,omitempty"`
	LinkedDests   []string `json:"linkedDests,omitempty"`
}

// Path returns the state file's path in the state directory.
func Path() (string, error) {
	stateDir, err := paths.State()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, fileName), nil
}

// Read loads the state file. A missing file is not an error: it returns
// empty state, meaning nothing is materialized yet.
func Read() (State, error) {
	path, err := Path()
	if err != nil {
		return State{}, err
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return State{Entries: map[Key]Entry{}}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("read state %s: %w", path, err)
	}

	var records []record
	if err := json.Unmarshal(data, &records); err != nil {
		return State{}, fmt.Errorf("parse state %s: %w", path, err)
	}

	entries := make(map[Key]Entry, len(records))
	for _, r := range records {
		entries[Key{Repo: r.Repo, Namespace: r.Namespace}] = Entry{
			Enabled:       r.Enabled,
			ActiveProfile: r.ActiveProfile,
			LinkedDests:   r.LinkedDests,
		}
	}
	return State{Entries: entries}, nil
}

// Write persists the state file, sorted by repo then namespace for stable
// diffs.
func Write(s State) error {
	path, err := Path()
	if err != nil {
		return err
	}

	records := make([]record, 0, len(s.Entries))
	for k, e := range s.Entries {
		records = append(records, record{
			Repo:          k.Repo,
			Namespace:     k.Namespace,
			Enabled:       e.Enabled,
			ActiveProfile: e.ActiveProfile,
			LinkedDests:   e.LinkedDests,
		})
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Repo != records[j].Repo {
			return records[i].Repo < records[j].Repo
		}
		return records[i].Namespace < records[j].Namespace
	})

	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write state %s: %w", path, err)
	}
	return nil
}
