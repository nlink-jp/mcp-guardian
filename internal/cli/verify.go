package cli

import (
	"fmt"

	"github.com/nlink-jp/mcp-guardian/internal/receipt"
)

// Verify checks the hash chain integrity of all receipt files.
// Each file is verified independently (each chain starts from "genesis").
// Returns a non-nil error if any chain is broken.
func Verify(stateDir string) error {
	intact, brokenFile, brokenAt, depth := receipt.VerifyChain(stateDir)

	if depth == 0 {
		fmt.Println("No receipts to verify.")
		return nil
	}

	if intact {
		fmt.Printf("%s✓ Hash chain intact%s (%d receipts)\n", green, reset, depth)
		return nil
	}

	fmt.Printf("%s✗ Hash chain BROKEN in %s at receipt #%d%s (of %d total)\n", red, brokenFile, brokenAt, reset, depth)
	return fmt.Errorf("hash chain broken in %s at receipt #%d", brokenFile, brokenAt)
}
