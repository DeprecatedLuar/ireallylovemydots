package manifest

import "testing"

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
