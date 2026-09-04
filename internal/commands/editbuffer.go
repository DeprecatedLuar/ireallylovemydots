package commands

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/DeprecatedLuar/dotz/internal/ui"
)

// editBuffer runs the $EDITOR loop shared by every manual-edit escape hatch
// (`namespace <ns> edit`, `namespace <ns> profiles edit`): seed is written to
// a temp file, $EDITOR runs against it, an unchanged buffer is a no-op, a
// changed one is validated on the way back in, and a validation failure
// holds the terminal for reopen-or-discard — non-interactively that failure
// is a hard error and the edit is discarded (concept.md "Manual edits").
// pathForDisplay names the real file in prompts and errors, not the temp
// buffer. commit receives the seed and the edited bytes and persists them;
// it is only called once edited has passed validate and differs from seed.
func editBuffer(seed []byte, pathForDisplay string, validate func(edited []byte) error, commit func(seed, edited []byte) error) error {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		return fmt.Errorf("$EDITOR is not set")
	}

	tmp, err := os.CreateTemp("", "dots-edit-*.toml")
	if err != nil {
		return fmt.Errorf("create edit buffer: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(seed); err != nil {
		tmp.Close()
		return fmt.Errorf("write edit buffer: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close edit buffer: %w", err)
	}

	for {
		cmd := exec.Command(editor, tmpPath)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("run %s: %w", editor, err)
		}

		edited, err := os.ReadFile(tmpPath)
		if err != nil {
			return fmt.Errorf("read edit buffer: %w", err)
		}

		if bytes.Equal(seed, edited) {
			return nil
		}

		if validateErr := validate(edited); validateErr != nil {
			if !ui.Interactive() {
				return fmt.Errorf("%s is invalid: %w (edit discarded)", pathForDisplay, validateErr)
			}
			choice, promptErr := ui.Prompt(
				"",
				fmt.Sprintf("%s is invalid: %v\n  [r] reopen at the error\n  [d] discard the edit", pathForDisplay, validateErr),
				[]string{"r", "d"},
			)
			if promptErr != nil {
				return promptErr
			}
			if strings.EqualFold(strings.TrimSpace(choice), "r") {
				continue
			}
			return nil
		}

		return commit(seed, edited)
	}
}
