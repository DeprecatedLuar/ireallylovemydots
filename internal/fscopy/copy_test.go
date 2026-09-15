package fscopy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopy_File_KeepsMode(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(src, []byte("hello"), 0700); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "dst.txt")
	if err := Copy(src, dst); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("dst content = %q, want %q", data, "hello")
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("dst mode = %v, want 0700", info.Mode().Perm())
	}
}

func TestCopy_Directory_CopiesRecursively(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(filepath.Join(src, "nested"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "top.txt"), []byte("top"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "nested", "deep.txt"), []byte("deep"), 0644); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "dst")
	if err := Copy(src, dst); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	top, err := os.ReadFile(filepath.Join(dst, "top.txt"))
	if err != nil || string(top) != "top" {
		t.Fatalf("top.txt = %q, err=%v", top, err)
	}
	deep, err := os.ReadFile(filepath.Join(dst, "nested", "deep.txt"))
	if err != nil || string(deep) != "deep" {
		t.Fatalf("nested/deep.txt = %q, err=%v", deep, err)
	}
}

func TestCopy_Symlink_CopiesAsSymlinkSameTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "link")
	if err := os.Symlink("target.txt", src); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "link-copy")
	if err := Copy(src, dst); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	info, err := os.Lstat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected dst to be a symlink")
	}
	linkText, err := os.Readlink(dst)
	if err != nil {
		t.Fatal(err)
	}
	if linkText != "target.txt" {
		t.Fatalf("link text = %q, want %q", linkText, "target.txt")
	}
}

func TestCopy_ExistingDst_Errors(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(src, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "dst.txt")
	if err := os.WriteFile(dst, []byte("already here"), 0644); err != nil {
		t.Fatal(err)
	}

	err := Copy(src, dst)
	if err == nil {
		t.Fatal("expected Copy to error on an existing dst")
	}

	data, readErr := os.ReadFile(dst)
	if readErr != nil || string(data) != "already here" {
		t.Fatalf("expected dst left untouched, got %q, err=%v", data, readErr)
	}
}
