package main

import (
	"errors"
	"reflect"
	"testing"
)

func noAmbiguity(name string) (string, error) {
	return "", errors.New("unexpected ambiguity for " + name)
}

func TestResolveRoute_Aliases(t *testing.T) {
	namespaces := []string{"neovim"}

	cases := []struct {
		name string
		args []string
		want route
	}{
		{
			name: "canonical",
			args: []string{"namespace", "neovim", "enable"},
			want: route{target: targetNamespace, args: []string{"neovim", "enable"}},
		},
		{
			name: "verb-first",
			args: []string{"enable", "neovim"},
			want: route{target: targetNamespace, args: []string{"neovim", "enable"}},
		},
		{
			name: "namespace-first",
			args: []string{"neovim", "enable"},
			want: route{target: targetNamespace, args: []string{"neovim", "enable"}},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveRoute(c.args, namespaces, nil, noAmbiguity)
			if err != nil {
				t.Fatalf("resolveRoute(%v) error: %v", c.args, err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("resolveRoute(%v) = %+v, want %+v", c.args, got, c.want)
			}
		})
	}
}

func TestResolveRoute_NamespaceMvBothSpellings(t *testing.T) {
	namespaces := []string{"neovim"}

	got1, err := resolveRoute([]string{"namespace", "mv", "neovim", "editors"}, namespaces, nil, noAmbiguity)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got1.target != targetNamespace {
		t.Fatalf("expected targetNamespace, got %v", got1.target)
	}

	got2, err := resolveRoute([]string{"namespace", "neovim", "mv", "editors"}, namespaces, nil, noAmbiguity)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got2.target != targetNamespace {
		t.Fatalf("expected targetNamespace, got %v", got2.target)
	}
	if !reflect.DeepEqual(got2.args, []string{"neovim", "mv", "editors"}) {
		t.Fatalf("unexpected args: %+v", got2.args)
	}
}

func TestResolveRoute_AmbiguousInteractive(t *testing.T) {
	namespaces := []string{"nvim"}
	repos := []string{"nvim"}

	choose := func(name string) (string, error) {
		if name != "nvim" {
			t.Fatalf("unexpected ambiguity target %q", name)
		}
		return "namespace", nil
	}

	got, err := resolveRoute([]string{"nvim", "enable"}, namespaces, repos, choose)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.target != targetNamespace {
		t.Fatalf("expected targetNamespace, got %v", got.target)
	}
}

func TestResolveRoute_AmbiguousNonInteractiveErrors(t *testing.T) {
	namespaces := []string{"nvim"}
	repos := []string{"nvim"}

	nonInteractive := func(name string) (string, error) {
		return "", errors.New(`"nvim" matches both a namespace and a repository`)
	}

	_, err := resolveRoute([]string{"nvim", "enable"}, namespaces, repos, nonInteractive)
	if err == nil {
		t.Fatal("expected error for ambiguous name in non-interactive mode")
	}
}

func TestResolveRoute_InitAlias(t *testing.T) {
	got, err := resolveRoute([]string{"init", "/some/path"}, nil, nil, noAmbiguity)
	if err != nil {
		t.Fatalf("resolveRoute(init) error: %v", err)
	}
	if got.target != targetRepo {
		t.Fatalf("expected targetRepo, got %v", got.target)
	}
	want := []string{"init", "/some/path"}
	if len(got.args) != len(want) {
		t.Fatalf("args = %v, want %v", got.args, want)
	}
	for i := range want {
		if got.args[i] != want[i] {
			t.Fatalf("args = %v, want %v", got.args, want)
		}
	}
}

func TestResolveRoute_UnknownToken(t *testing.T) {
	_, err := resolveRoute([]string{"bogus", "enable"}, nil, nil, noAmbiguity)
	if err == nil {
		t.Fatal("expected error for unknown token")
	}
}

func TestResolveRoute_TopLevel(t *testing.T) {
	cases := []struct {
		args []string
		want target
	}{
		{nil, targetList},
		{[]string{"list"}, targetList},
		{[]string{"status"}, targetList},
		{[]string{"sync"}, targetSync},
		{[]string{"repo"}, targetRepo},
		{[]string{"init"}, targetRepo},
		{[]string{"namespace"}, targetNamespace},
		{[]string{"help"}, targetHelp},
	}
	for _, c := range cases {
		got, err := resolveRoute(c.args, nil, nil, noAmbiguity)
		if err != nil {
			t.Fatalf("resolveRoute(%v) error: %v", c.args, err)
		}
		if got.target != c.want {
			t.Fatalf("resolveRoute(%v) target = %v, want %v", c.args, got.target, c.want)
		}
	}
}
