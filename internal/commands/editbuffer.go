package commands

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/DeprecatedLuar/ireallylovemydots/internal/ui"
)

// editOutcome is how an editBuffer session ended.
type editOutcome int

const (
	editUnchanged editOutcome = iota
	editCommitted
	editDiscarded
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
// The outcome is editUnchanged for an identical buffer, editDiscarded when
// the user discards a failed edit, and editCommitted once commit succeeds.
func editBuffer(seed []byte, pathForDisplay string, validate func(edited []byte) error, commit func(seed, edited []byte) error) (editOutcome, error) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		return editUnchanged, fmt.Errorf("$EDITOR is not set")
	}

	tmp, err := os.CreateTemp("", "dots-edit-*.toml")
	if err != nil {
		return editUnchanged, fmt.Errorf("create edit buffer: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(seed); err != nil {
		tmp.Close()
		return editUnchanged, fmt.Errorf("write edit buffer: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return editUnchanged, fmt.Errorf("close edit buffer: %w", err)
	}

	for {
		cmd := exec.Command(editor, tmpPath)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return editUnchanged, fmt.Errorf("run %s: %w", editor, err)
		}

		edited, err := os.ReadFile(tmpPath)
		if err != nil {
			return editUnchanged, fmt.Errorf("read edit buffer: %w", err)
		}

		if bytes.Equal(seed, edited) {
			return editUnchanged, nil
		}

		if validateErr := validate(edited); validateErr != nil {
			if !ui.Interactive() {
				return editUnchanged, fmt.Errorf("%s is invalid: %w (edit discarded)", pathForDisplay, validateErr)
			}
			choice, promptErr := ui.Prompt(
				"",
				fmt.Sprintf("%s is invalid: %v\n  [r] reopen at the error\n  [d] discard the edit", pathForDisplay, validateErr),
				[]string{"r", "d"},
			)
			if promptErr != nil {
				return editUnchanged, promptErr
			}
			if strings.EqualFold(strings.TrimSpace(choice), "r") {
				continue
			}
			return editDiscarded, nil
		}

		return editCommitted, commit(seed, edited)
	}
}
