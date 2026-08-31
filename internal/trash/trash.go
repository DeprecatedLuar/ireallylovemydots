// Package trash implements the XDG Trash spec for arbitrary files and
// directories. dots never unlinks anything outright; anything destructive
// passes through here first.
package trash

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	dirName   = "Trash"
	filesDir  = "files"
	infoDir   = "info"
	infoExt   = ".trashinfo"
	dirPerm   = 0700
	filePerm  = 0644
	timeStamp = "2006-01-02T15:04:05"
)

// dir returns the home trash directory, $XDG_DATA_HOME/Trash, creating its
// files/ and info/ subdirectories if absent.
func dir() (string, error) {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	trashDir := filepath.Join(dataHome, dirName)
	for _, sub := range []string{filesDir, infoDir} {
		if err := os.MkdirAll(filepath.Join(trashDir, sub), dirPerm); err != nil {
			return "", fmt.Errorf("create %s: %w", sub, err)
		}
	}
	return trashDir, nil
}

// Move sends path to the XDG trash and returns the name it was filed under,
// which is what Restore needs to bring it back.
func Move(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path for %s: %w", path, err)
	}
	if _, err := os.Lstat(abs); err != nil {
		return "", fmt.Errorf("stat %s: %w", abs, err)
	}

	trashDir, err := dir()
	if err != nil {
		return "", err
	}

	name := uniqueName(trashDir, filepath.Base(abs))
	trashedPath := filepath.Join(trashDir, filesDir, name)
	infoPath := filepath.Join(trashDir, infoDir, name+infoExt)

	if err := os.Rename(abs, trashedPath); err != nil {
		return "", fmt.Errorf("move %s to trash: %w", abs, err)
	}

	content := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n",
		abs, time.Now().Format(timeStamp))
	if err := os.WriteFile(infoPath, []byte(content), filePerm); err != nil {
		os.Rename(trashedPath, abs) // best-effort rollback
		return "", fmt.Errorf("write trashinfo for %s: %w", abs, err)
	}

	return name, nil
}

// Restore moves a trashed item, identified by the name Move returned, back
// to its original location.
func Restore(name, destination string) error {
	trashDir, err := dir()
	if err != nil {
		return err
	}

	trashedPath := filepath.Join(trashDir, filesDir, name)
	infoPath := filepath.Join(trashDir, infoDir, name+infoExt)

	if _, err := os.Lstat(trashedPath); err != nil {
		return fmt.Errorf("stat trashed item %s: %w", name, err)
	}
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("restore destination %s already exists", destination)
	}

	if err := os.MkdirAll(filepath.Dir(destination), dirPerm); err != nil {
		return fmt.Errorf("create parent directory for %s: %w", destination, err)
	}
	if err := os.Rename(trashedPath, destination); err != nil {
		return fmt.Errorf("restore %s: %w", name, err)
	}

	os.Remove(infoPath) // best-effort; item is already restored
	return nil
}

// Purge permanently deletes a trashed item, identified by the name Move
// returned, bypassing the recovery Restore would otherwise offer. This is
// the one operation in the trash package that makes something
// unrecoverable — concept.md "--purge erases instead of trashing": dots
// always trashes first (which is what makes a transactional rollback
// possible mid-operation), and only erases via this call once the whole
// operation has already succeeded.
func Purge(name string) error {
	trashDir, err := dir()
	if err != nil {
		return err
	}
	trashedPath := filepath.Join(trashDir, filesDir, name)
	infoPath := filepath.Join(trashDir, infoDir, name+infoExt)

	if err := os.RemoveAll(trashedPath); err != nil {
		return fmt.Errorf("erase trashed item %s: %w", name, err)
	}
	os.Remove(infoPath) // best-effort; the item itself is already gone
	return nil
}

// uniqueName appends a numeric suffix if base is already trashed, per the
// XDG spec's collision rule.
func uniqueName(trashDir, base string) string {
	candidate := base
	for i := 1; ; i++ {
		if _, err := os.Lstat(filepath.Join(trashDir, filesDir, candidate)); os.IsNotExist(err) {
			return candidate
		}
		ext := filepath.Ext(base)
		stem := strings.TrimSuffix(base, ext)
		candidate = fmt.Sprintf("%s_%d%s", stem, i, ext)
	}
}
