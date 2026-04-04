package receipt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLedgerAppendAndVerify(t *testing.T) {
	dir := t.TempDir()

	ledger, err := NewLedger(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Append 3 records
	for i := 0; i < 3; i++ {
		r := &Record{
			Timestamp:    int64(1700000000000 + i*1000),
			ToolName:     "test_tool",
			Arguments:    map[string]interface{}{"index": float64(i)},
			Target:       "/tmp/test",
			MutationType: "readonly",
			Outcome:      "success",
			DurationMs:   10,
		}
		if err := ledger.Append(r); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	if ledger.Seq() != 3 {
		t.Fatalf("expected seq 3, got %d", ledger.Seq())
	}

	// Verify chain
	path := filepath.Join(dir, "receipts.jsonl")
	intact, brokenAt, depth := VerifyChain(path)
	if !intact {
		t.Fatalf("chain broken at %d", brokenAt)
	}
	if depth != 3 {
		t.Fatalf("expected depth 3, got %d", depth)
	}

	// Tamper with a record and verify again
	records, _ := LoadReceipts(path)
	records[1].Hash = "tampered"
	// Rewrite the file with tampered data
	f, _ := os.Create(path)
	for _, r := range records {
		data, _ := json.Marshal(r)
		f.Write(append(data, '\n'))
	}
	f.Close()

	intact, brokenAt, _ = VerifyChain(path)
	if intact {
		t.Fatal("expected chain to be broken after tampering")
	}
	if brokenAt != 2 {
		t.Fatalf("expected break at seq 2, got %d", brokenAt)
	}
}
