package repo

import "testing"

func TestInspect_Empty(t *testing.T) {
	if got := Inspect(nil); got != StateEmpty {
		t.Fatalf("Inspect(nil) = %v, want StateEmpty", got)
	}
}

func TestInspect_Namespaces(t *testing.T) {
	entries := []RootEntry{
		{Name: "editors", IsDir: true, HasDots: true},
		{Name: "README.md"},
	}
	if got := Inspect(entries); got != StateNamespaces {
		t.Fatalf("Inspect(%+v) = %v, want StateNamespaces", entries, got)
	}
}

func TestInspect_Incompatible(t *testing.T) {
	entries := []RootEntry{
		{Name: "src", IsDir: true},
		{Name: "README.md"},
	}
	if got := Inspect(entries); got != StateIncompatible {
		t.Fatalf("Inspect(%+v) = %v, want StateIncompatible", entries, got)
	}
}
