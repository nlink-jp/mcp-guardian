package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nlink-jp/mcp-guardian/internal/receipt"
)

// Verify checks the hash chain integrity of the receipt ledger.
func Verify(stateDir string) {
	path := filepath.Join(stateDir, "receipts.jsonl")
	intact, brokenAt, depth := receipt.VerifyChain(path)

	if depth == 0 {
		fmt.Println("No receipts to verify.")
		return
	}

	if intact {
		fmt.Printf("%s✓ Hash chain intact%s (%d receipts)\n", green, reset, depth)
	} else {
		fmt.Printf("%s✗ Hash chain BROKEN at receipt #%d%s (of %d)\n", red, brokenAt, reset, depth)
		os.Exit(1)
	}
}
