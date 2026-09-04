package commands

import (
	"fmt"
	"os"

	"github.com/DeprecatedLuar/dotz/internal/selfheal"
)

// emptyRegistryHint is the one line concept.md "Listing output" requires
// when a listing runs against an empty registry: silence would otherwise be
// indistinguishable from "registered repositories, nothing enabled".
const emptyRegistryHint = "No repositories registered. Run: dots repo add <url>"

// HandleList implements the top-level list and its status alias as shortcuts
// for namespace list. Its scope is every namespace in every registered
// repository, materialized or not.
func HandleList(args []string, findings selfheal.Findings) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: list")
	}
	return renderNamespaceList(findings)
}

// printEmptyRegistryHint prints the hint to stderr, per concept.md: exit
// status stays zero and stdout stays empty, so pipes and scripts are
// unaffected. It appears only while the registry is empty.
func printEmptyRegistryHint() {
	fmt.Fprintln(os.Stderr, emptyRegistryHint)
}
