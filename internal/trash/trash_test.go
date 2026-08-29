package trash

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMoveAndRestore_File(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)

	src := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(src, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	name, err := Move(src)
	if err != nil {
		t.Fatalf("Move error: %v", err)
	}
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be gone after Move", src)
	}

	if err := Restore(name, src); err != nil {
		t.Fatalf("Restore error: %v", err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("expected %s to be restored: %v", src, err)
	}
	if string(data) != "hello" {
		t.Fatalf("got content %q, want %q", data, "hello")
	}
}

func TestMoveAndRestore_Directory(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)

	src := filepath.Join(t.TempDir(), "nvim")
	if err := os.MkdirAll(filepath.Join(src, "lua"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "lua", "init.lua"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	name, err := Move(src)
	if err != nil {
		t.Fatalf("Move error: %v", err)
	}
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be gone after Move", src)
	}

	if err := Restore(name, src); err != nil {
		t.Fatalf("Restore error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(src, "lua", "init.lua")); err != nil {
		t.Fatalf("expected restored directory to keep contents: %v", err)
	}
}
