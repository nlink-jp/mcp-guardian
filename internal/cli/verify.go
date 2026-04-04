package cli

import (
	"fmt"
	"path/filepath"

	"github.com/nlink-jp/mcp-guardian/internal/receipt"
)

// Verify checks the hash chain integrity of the receipt ledger.
// Returns a non-nil error if the chain is broken.
func Verify(stateDir string) error {
	path := filepath.Join(stateDir, "receipts.jsonl")
	intact, brokenAt, depth := receipt.VerifyChain(path)

	if depth == 0 {
		fmt.Println("No receipts to verify.")
		return nil
	}

	if intact {
		fmt.Printf("%s✓ Hash chain intact%s (%d receipts)\n", green, reset, depth)
		return nil
	}

	fmt.Printf("%s✗ Hash chain BROKEN at receipt #%d%s (of %d)\n", red, brokenAt, reset, depth)
	return fmt.Errorf("hash chain broken at receipt #%d", brokenAt)
}
