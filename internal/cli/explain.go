package cli

import (
	"fmt"
	"path/filepath"

	"github.com/nlink-jp/mcp-guardian/internal/receipt"
)

// Explain generates a plain-language summary of the session.
func Explain(stateDir string) error {
	path := filepath.Join(stateDir, "receipts.jsonl")
	records, err := receipt.LoadReceipts(path)
	if err != nil {
		return fmt.Errorf("cannot read receipts: %w", err)
	}

	if len(records) == 0 {
		fmt.Println("No activity to explain.")
		return nil
	}

	// Gather statistics
	toolCounts := make(map[string]int)
	var successes, errors, blocked int
	var totalDuration int64
	targets := make(map[string]bool)

	for _, r := range records {
		toolCounts[r.ToolName]++
		switch r.Outcome {
		case "success":
			successes++
		case "error":
			errors++
		case "blocked":
			blocked++
		}
		totalDuration += r.DurationMs
		if r.Target != "" {
			targets[r.Target] = true
		}
	}

	fmt.Printf("%sSession Summary%s\n", bold, reset)
	fmt.Printf("  Total calls: %d (%d success, %d errors, %d blocked)\n",
		len(records), successes, errors, blocked)
	fmt.Printf("  Total duration: %dms\n", totalDuration)
	fmt.Printf("  Unique targets: %d\n", len(targets))
	fmt.Println()

	fmt.Printf("%sTool Usage%s\n", bold, reset)
	for tool, count := range toolCounts {
		fmt.Printf("  %s: %d calls\n", tool, count)
	}
	fmt.Println()

	// Narrative
	fmt.Printf("%sNarrative%s\n", bold, reset)
	if blocked > 0 {
		fmt.Printf("  The governance proxy blocked %d call(s) during this session.\n", blocked)
	}
	if errors > 0 {
		fmt.Printf("  %d call(s) resulted in errors from the upstream server.\n", errors)
	}
	if successes > 0 {
		fmt.Printf("  %d call(s) completed successfully.\n", successes)
	}

	// List blocked/error details
	for _, r := range records {
		if r.Outcome == "blocked" || r.Outcome == "error" {
			fmt.Printf("  - %s%s%s on %s: %s\n",
				red, r.ToolName, reset, r.Target, r.Summary)
		}
	}
	return nil
}

// Receipts shows a compact session summary.
func Receipts(stateDir string) error {
	path := filepath.Join(stateDir, "receipts.jsonl")
	records, err := receipt.LoadReceipts(path)
	if err != nil {
		return fmt.Errorf("cannot read receipts: %w", err)
	}

	if len(records) == 0 {
		fmt.Println("No receipts.")
		return nil
	}

	var successes, errors, blocked int
	for _, r := range records {
		switch r.Outcome {
		case "success":
			successes++
		case "error":
			errors++
		case "blocked":
			blocked++
		}
	}

	fmt.Printf("Receipts: %d total | %s%d success%s | %s%d errors%s | %s%d blocked%s\n",
		len(records),
		green, successes, reset,
		red, errors, reset,
		yellow, blocked, reset,
	)
	if len(records) > 0 {
		last := records[len(records)-1]
		fmt.Printf("Chain head: %s...%s\n", last.Hash[:16], last.Hash[len(last.Hash)-8:])
	}
	return nil
}
