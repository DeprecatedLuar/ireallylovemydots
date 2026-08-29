package link

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClassify_Missing(t *testing.T) {
	dir := t.TempDir()
	state, err := Classify(filepath.Join(dir, "nope"), "/anything")
	if err != nil {
		t.Fatalf("Classify error: %v", err)
	}
	if state != Missing {
		t.Fatalf("got %v, want Missing", state)
	}
}

func TestClassify_CorrectSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real")
	os.WriteFile(target, []byte("x"), 0644)
	link := filepath.Join(dir, "link")
	if err := Create(link, target); err != nil {
		t.Fatal(err)
	}

	state, err := Classify(link, target)
	if err != nil {
		t.Fatalf("Classify error: %v", err)
	}
	if state != CorrectSymlink {
		t.Fatalf("got %v, want CorrectSymlink", state)
	}
}

func TestClassify_WrongSymlink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "link")
	if err := Create(link, filepath.Join(dir, "actual")); err != nil {
		t.Fatal(err)
	}

	state, err := Classify(link, filepath.Join(dir, "expected"))
	if err != nil {
		t.Fatalf("Classify error: %v", err)
	}
	if state != WrongSymlink {
		t.Fatalf("got %v, want WrongSymlink", state)
	}
}

func TestClassify_RealFileAndDir(t *testing.T) {
	dir := t.TempDir()

	file := filepath.Join(dir, "file")
	os.WriteFile(file, []byte("x"), 0644)
	state, err := Classify(file, "/whatever")
	if err != nil {
		t.Fatal(err)
	}
	if state != RealFile {
		t.Fatalf("got %v, want RealFile", state)
	}

	subdir := filepath.Join(dir, "subdir")
	os.Mkdir(subdir, 0700)
	state, err = Classify(subdir, "/whatever")
	if err != nil {
		t.Fatal(err)
	}
	if state != RealDir {
		t.Fatalf("got %v, want RealDir", state)
	}
}

func TestCreateRemoveRead(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")

	if err := Create(link, target); err != nil {
		t.Fatalf("Create error: %v", err)
	}
	got, err := Read(link)
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if got != target {
		t.Fatalf("got target %q, want %q", got, target)
	}
	if err := Remove(link); err != nil {
		t.Fatalf("Remove error: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("expected link to be gone after Remove")
	}
}
