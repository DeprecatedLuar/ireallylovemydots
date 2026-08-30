package engine

import (
	"testing"

	"github.com/DeprecatedLuar/dotz/internal/state"
)

func TestBuildIndex_SkipsDisabledNamespaces(t *testing.T) {
	s := state.State{Entries: map[state.Key]state.Entry{
		{Repo: "dotfiles", Namespace: "nvim"}:  {Enabled: true, LinkedDests: []string{"/home/u/.config/nvim"}},
		{Repo: "dotfiles", Namespace: "kitty"}: {Enabled: false, LinkedDests: []string{"/home/u/.config/kitty"}},
	}}
	idx := BuildIndex(s)

	if _, ok := idx.Conflict("/home/u/.config/nvim"); !ok {
		t.Fatal("expected the enabled namespace's destination to be claimed")
	}
	if _, ok := idx.Conflict("/home/u/.config/kitty"); ok {
		t.Fatal("expected the disabled namespace's destination to be unclaimed")
	}
}

func TestIndex_Conflict_PrefixAwareBothDirections(t *testing.T) {
	s := state.State{Entries: map[state.Key]state.Entry{
		{Repo: "dotfiles", Namespace: "nvim"}: {Enabled: true, LinkedDests: []string{"/home/u/.config/nvim"}},
	}}
	idx := BuildIndex(s)

	key, ok := idx.Conflict("/home/u/.config/nvim/init.lua")
	if !ok || key.Namespace != "nvim" {
		t.Fatalf("expected a child path beneath a claimed directory to conflict, got key=%+v ok=%v", key, ok)
	}

	if _, ok := idx.Conflict("/home/u/.config/kitty"); ok {
		t.Fatal("expected an unrelated destination not to conflict")
	}

	s2 := state.State{Entries: map[state.Key]state.Entry{
		{Repo: "dotfiles", Namespace: "shell"}: {Enabled: true, LinkedDests: []string{"/home/u/.config/nvim/init.lua"}},
	}}
	idx2 := BuildIndex(s2)
	if _, ok := idx2.Conflict("/home/u/.config/nvim"); !ok {
		t.Fatal("expected a parent directory to conflict with an already-claimed child path")
	}
}

func TestIndex_Conflict_ExactMatch(t *testing.T) {
	s := state.State{Entries: map[state.Key]state.Entry{
		{Repo: "dotfiles", Namespace: "nvim"}: {Enabled: true, LinkedDests: []string{"/home/u/.config/nvim"}},
	}}
	idx := BuildIndex(s)
	key, ok := idx.Conflict("/home/u/.config/nvim")
	if !ok || key.Namespace != "nvim" {
		t.Fatalf("expected exact destination match to conflict, got key=%+v ok=%v", key, ok)
	}
}
