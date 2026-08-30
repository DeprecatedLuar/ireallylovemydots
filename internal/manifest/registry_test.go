package manifest

import (
	"os"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestWriteRegistry_IsValidTOML(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)

	r := Registry{Repos: []Repo{
		{Name: "dotfiles", Owner: "someone", URL: "https://example.com/someone/dotfiles"},
	}}
	if err := WriteRegistry(r); err != nil {
		t.Fatalf("WriteRegistry error: %v", err)
	}

	path, err := RegistryPath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, ".toml") {
		t.Fatalf("registry path %q does not carry a .toml suffix", path)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Registry
	if _, err := toml.Decode(string(raw), &decoded); err != nil {
		t.Fatalf("on-disk registry is not valid TOML: %v", err)
	}
}

func TestRegistryRoundTrip_SortedByName(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)

	r := Registry{Repos: []Repo{
		{Name: "zdots", URL: "https://example.com/z/zdots"},
		{Name: "adots", URL: "https://example.com/a/adots"},
	}}
	if err := WriteRegistry(r); err != nil {
		t.Fatalf("WriteRegistry error: %v", err)
	}

	got, err := ReadRegistry()
	if err != nil {
		t.Fatalf("ReadRegistry error: %v", err)
	}
	if len(got.Repos) != 2 || got.Repos[0].Name != "adots" || got.Repos[1].Name != "zdots" {
		t.Fatalf("got %+v, want sorted adots, zdots", got.Repos)
	}
}

func TestReadRegistry_MissingFileIsEmpty(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	got, err := ReadRegistry()
	if err != nil {
		t.Fatalf("ReadRegistry error: %v", err)
	}
	if len(got.Repos) != 0 {
		t.Fatalf("got %d repos, want 0", len(got.Repos))
	}
}
