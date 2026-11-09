package ui

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStartSpinner_NilWhenNotATerminal(t *testing.T) {
	if isTerminal(os.Stdout) {
		t.Skip("stdout is a terminal")
	}
	s := StartSpinner("repo")
	if s != nil {
		t.Fatal("expected no spinner without a terminal on stdout")
	}
	s.Stop()
}

func TestSpinner_StopClearsLineAndIsIdempotent(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	s := newSpinner()
	go s.run("repo", false)
	time.Sleep(2 * spinnerInterval)
	s.Stop()
	s.Stop()
	os.Stdout = old
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "| repo") || !strings.HasSuffix(got, clearLine) {
		t.Fatalf("output = %q, want frames ending in a cleared line", got)
	}
}
