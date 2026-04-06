package cli

import (
	"fmt"
	"time"

	"github.com/nlink-jp/mcp-guardian/internal/receipt"
)

// View displays a receipt timeline with optional filtering.
// Aggregates records from all receipt files, sorted by timestamp.
func View(stateDir, filterTool, filterOutcome string, limit int) error {
	records, err := receipt.LoadAllReceipts(stateDir)
	if err != nil {
		return fmt.Errorf("cannot read receipts: %w", err)
	}

	if len(records) == 0 {
		fmt.Println("No receipts found.")
		return nil
	}

	count := 0
	for _, r := range records {
		if filterTool != "" && r.ToolName != filterTool {
			continue
		}
		if filterOutcome != "" && r.Outcome != filterOutcome {
			continue
		}
		if limit > 0 && count >= limit {
			break
		}

		printReceipt(r)
		count++
	}

	fmt.Printf("\n%s%d receipt(s) shown%s\n", gray, count, reset)
	return nil
}

func printReceipt(r *receipt.Record) {
	ts := time.UnixMilli(r.Timestamp).Format("2006-01-02 15:04:05")

	outcomeColor := green
	switch r.Outcome {
	case "error":
		outcomeColor = red
	case "blocked":
		outcomeColor = yellow
	}

	fmt.Printf("%s#%d%s %s%s%s %s%s%s [%s%s%s] %s(%dms)%s\n",
		gray, r.Seq, reset,
		cyan, ts, reset,
		bold, r.ToolName, reset,
		outcomeColor, r.Outcome, reset,
		gray, r.DurationMs, reset,
	)

	if r.Target != "" {
		fmt.Printf("     target: %s\n", r.Target)
	}
	if r.MutationType != "" {
		fmt.Printf("     mutation: %s\n", r.MutationType)
	}
	if r.ErrorText != "" {
		fmt.Printf("     %serror: %s%s\n", red, r.ErrorText, reset)
	}
	if r.ConvergenceSignal != "" && r.ConvergenceSignal != "none" {
		fmt.Printf("     %sconvergence: %s%s\n", yellow, r.ConvergenceSignal, reset)
	}
	fmt.Println()
}
