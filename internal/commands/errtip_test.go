package commands

import (
	"errors"
	"testing"

	"github.com/DeprecatedLuar/ireallylovemydots/internal/ui"
)

// errWithTip renders err as the user sees it: the message, then the tip line
// when the error carries one.
func errWithTip(err error) string {
	var te *ui.TipError
	if errors.As(err, &te) {
		return err.Error() + "\n" + te.Tip
	}
	return err.Error()
}

func TestErrWithTip_JoinsMessageAndTip(t *testing.T) {
	err := ui.WithTip(errors.New("nope"), "try this")
	if got := errWithTip(err); got != "nope\ntry this" {
		t.Fatalf("got %q", got)
	}
	if got := errWithTip(errors.New("plain")); got != "plain" {
		t.Fatalf("got %q", got)
	}
}
