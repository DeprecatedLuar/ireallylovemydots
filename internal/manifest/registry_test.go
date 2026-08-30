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

func TestRegistry_LocalOriginWritesToStateDirNotConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	r := Registry{Repos: []Repo{
		{Name: "remote-one", Owner: "someone", URL: "https://example.com/someone/remote-one"},
		{Name: "local-one", Origin: OriginLocal},
	}}
	if err := WriteRegistry(r); err != nil {
		t.Fatalf("WriteRegistry: %v", err)
	}

	configPath, err := RegistryPath()
	if err != nil {
		t.Fatal(err)
	}
	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config registry: %v", err)
	}
	if strings.Contains(string(configData), "local-one") {
		t.Fatalf("expected the local repo repo init produced to be absent from the shared config registry, got:\n%s", configData)
	}

	localPath, err := LocalRegistryPath()
	if err != nil {
		t.Fatal(err)
	}
	localData, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("read local registry: %v", err)
	}
	if !strings.Contains(string(localData), "local-one") {
		t.Fatalf("expected the local repo written to the state-directory registry, got:\n%s", localData)
	}

	got, err := ReadRegistry()
	if err != nil {
		t.Fatalf("ReadRegistry: %v", err)
	}
	if len(got.Repos) != 2 {
		t.Fatalf("expected the union of both files, got %+v", got.Repos)
	}
	for _, repo := range got.Repos {
		switch repo.Name {
		case "remote-one":
			if repo.Origin != OriginConfig {
				t.Fatalf("expected remote-one tagged OriginConfig, got %v", repo.Origin)
			}
		case "local-one":
			if repo.Origin != OriginLocal {
				t.Fatalf("expected local-one tagged OriginLocal, got %v", repo.Origin)
			}
		}
	}
}

func TestRegistry_RmFollowsOriginTag(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	initial := Registry{Repos: []Repo{
		{Name: "remote-one", URL: "https://example.com/someone/remote-one"},
		{Name: "local-one", Origin: OriginLocal},
	}}
	if err := WriteRegistry(initial); err != nil {
		t.Fatalf("WriteRegistry: %v", err)
	}

	reg, err := ReadRegistry()
	if err != nil {
		t.Fatalf("ReadRegistry: %v", err)
	}
	var remaining []Repo
	for _, repo := range reg.Repos {
		if repo.Name != "local-one" {
			remaining = append(remaining, repo)
		}
	}
	if err := WriteRegistry(Registry{Repos: remaining}); err != nil {
		t.Fatalf("WriteRegistry: %v", err)
	}

	localPath, err := LocalRegistryPath()
	if err != nil {
		t.Fatal(err)
	}
	localData, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("read local registry: %v", err)
	}
	if strings.Contains(string(localData), "local-one") {
		t.Fatalf("expected local-one removed from the local registry, got:\n%s", localData)
	}

	got, err := ReadRegistry()
	if err != nil {
		t.Fatalf("ReadRegistry: %v", err)
	}
	if len(got.Repos) != 1 || got.Repos[0].Name != "remote-one" {
		t.Fatalf("expected only remote-one left, got %+v", got.Repos)
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
