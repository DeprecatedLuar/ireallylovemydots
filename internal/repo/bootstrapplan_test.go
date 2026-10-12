package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeprecatedLuar/ireallylovemydots/internal/manifest"
)

var planEntries = []RootEntry{
	{Name: ".git", IsDir: true}, {Name: ".gitignore"}, {Name: "README.md"},
	{Name: "nvim", IsDir: true}, {Name: ".bashrc"}, {Name: "shell", IsDir: true, HasDots: true},
}

func TestDefaultBootstrapPlan_EverythingStaysAtRoot(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	p, err := DefaultBootstrapPlan("dotfiles", planEntries)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "dotfiles" || len(p.Namespaces) != 0 {
		t.Fatalf("plan = %+v", p)
	}
	want := map[string]string{"README.md": "none", "nvim": "~/.config/nvim", ".bashrc": "~/.bashrc"}
	if len(p.Root) != len(want) {
		t.Fatalf("root = %v, want %v", p.Root, want)
	}
	for k, v := range want {
		if p.Root[k] != v {
			t.Fatalf("root[%q] = %q, want %q", k, p.Root[k], v)
		}
	}
}

func TestBootstrapPlan_RoundTripsAndQuotesKeys(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	p, err := DefaultBootstrapPlan("dotfiles", planEntries)
	if err != nil {
		t.Fatal(err)
	}
	data := EncodeBootstrapPlan(p)
	if !strings.Contains(string(data), `".bashrc" = "~/.bashrc"`) {
		t.Fatalf("keys must be quoted:\n%s", data)
	}
	got, err := DecodeBootstrapPlan(data, planEntries)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != p.Name || len(got.Root) != len(p.Root) {
		t.Fatalf("round trip = %+v, want %+v", got, p)
	}
}

func TestDecodeBootstrapPlan_Rejects(t *testing.T) {
	cases := map[string]string{
		"entry in both tables": "name = \"d\"\n[root]\n\"nvim\" = \"~/x\"\n[namespaces]\n\"nvim\" = \"~/x\"\n",
		"unknown entry":        "name = \"d\"\n[root]\n\"ghost\" = \"~/x\"\n",
		"missing name":         "name = \"\"\n[root]\n\"nvim\" = \"~/x\"\n",
		"reserved name":        "name = \"list\"\n[root]\n\"nvim\" = \"~/x\"\n",
		"empty destination":    "name = \"d\"\n[root]\n\"nvim\" = \"\"\n",
		"namespace collision":  "[namespaces]\n\".bashrc\" = \"~/.bashrc\"\n\"bashrc\" = \"~/b\"\n",
		"unknown key":          "name = \"d\"\nbogus = 1\n",
	}
	entries := append(append([]RootEntry{}, planEntries...), RootEntry{Name: "bashrc"})
	for label, buf := range cases {
		if _, err := DecodeBootstrapPlan([]byte(buf), entries); err == nil {
			t.Errorf("%s: expected a validation error", label)
		}
	}
}

func TestApplyBootstrap_WritesRootDotsWithOverridesOnly(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	dir := t.TempDir()
	for _, f := range []string{"init.lua", "README.md"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	p := BootstrapPlan{Name: "nvim", Dest: "~/.config/nvim",
		Root: map[string]string{"init.lua": "~/.config/nvim/init.lua", "README.md": "none"}}
	if err := ApplyBootstrap(dir, p, []RootEntry{{Name: "init.lua"}, {Name: "README.md"}}); err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "nvim" || m.Dest != "/home/u/.config/nvim" || len(m.Entries) != 1 || m.Entries[0].Name != "README.md" {
		t.Fatalf("root manifest = %+v, want name, dest, and only the README override", m)
	}
}

func TestDecodeBootstrapPlan_RefusesRootNameCollisions(t *testing.T) {
	cases := map[string]struct{ buf, want string }{
		"moving namespace": {
			buf:  "name = \"nvim\"\n[root]\n\"README.md\" = \"none\"\n[namespaces]\n\"nvim\" = \"~/.config/nvim\"\n",
			want: "collides with namespace \"nvim\"",
		},
		"existing folder": {
			buf:  "name = \"shell\"\n[root]\n\"README.md\" = \"none\"\n",
			want: "\"shell\" is already a namespace folder",
		},
	}
	for label, c := range cases {
		_, err := DecodeBootstrapPlan([]byte(c.buf), planEntries)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to mention %q", label, err, c.want)
		}
	}
}
