package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func scriptedEditor(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "editor.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDITOR", path)
}

func TestEditBuffer_UnchangedReportsUnchanged(t *testing.T) {
	scriptedEditor(t, "exit 0")
	outcome, err := editBuffer([]byte("a"), "x", func([]byte) error { return nil }, func(_, _ []byte) error {
		t.Fatal("commit must not run for an unchanged buffer")
		return nil
	})
	if err != nil || outcome != editUnchanged {
		t.Fatalf("outcome = %v, err = %v", outcome, err)
	}
}

func TestEditBuffer_ChangedReportsCommitted(t *testing.T) {
	scriptedEditor(t, `printf b > "$1"`)
	outcome, err := editBuffer([]byte("a"), "x", func([]byte) error { return nil }, func(_, _ []byte) error { return nil })
	if err != nil || outcome != editCommitted {
		t.Fatalf("outcome = %v, err = %v", outcome, err)
	}
}
