package receipt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestNewLedger_TailRead(t *testing.T) {
	dir := t.TempDir()

	// Create a ledger and append records
	l1, _ := NewLedger(dir)
	for i := 0; i < 10; i++ {
		l1.Append(&Record{
			Timestamp:    int64(1700000000000 + i*1000),
			ToolName:     "test",
			MutationType: "readonly",
			Outcome:      "success",
		})
	}
	lastHash := l1.LastHash()
	lastSeq := l1.Seq()

	// Create a new ledger — should recover from tail read only
	l2, err := NewLedger(dir)
	if err != nil {
		t.Fatal(err)
	}
	if l2.Seq() != lastSeq {
		t.Errorf("seq=%d, want %d", l2.Seq(), lastSeq)
	}
	if l2.LastHash() != lastHash {
		t.Errorf("lastHash=%q, want %q", l2.LastHash(), lastHash)
	}
}

func TestNewLedger_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	// Create empty file
	os.WriteFile(filepath.Join(dir, "receipts.jsonl"), []byte{}, 0644)

	l, err := NewLedger(dir)
	if err != nil {
		t.Fatal(err)
	}
	if l.Seq() != 0 {
		t.Errorf("seq=%d, want 0", l.Seq())
	}
	if l.LastHash() != "genesis" {
		t.Errorf("lastHash=%q, want genesis", l.LastHash())
	}
}

func TestPurge_RemovesOldRecords(t *testing.T) {
	dir := t.TempDir()
	l, _ := NewLedger(dir)

	now := time.Now()

	// 5 old records (3 days ago)
	for i := 0; i < 5; i++ {
		l.Append(&Record{
			Timestamp:    now.Add(-72 * time.Hour).UnixMilli(),
			ToolName:     "old_tool",
			MutationType: "readonly",
			Outcome:      "success",
		})
	}
	// 3 recent records (1 hour ago)
	for i := 0; i < 3; i++ {
		l.Append(&Record{
			Timestamp:    now.Add(-1 * time.Hour).UnixMilli(),
			ToolName:     "new_tool",
			MutationType: "readonly",
			Outcome:      "success",
		})
	}

	if l.Seq() != 8 {
		t.Fatalf("seq=%d before purge, want 8", l.Seq())
	}

	// Purge records older than 2 days
	purged, err := l.Purge(48 * time.Hour)
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if purged != 5 {
		t.Errorf("purged=%d, want 5", purged)
	}

	// Verify remaining records
	records, _ := l.LoadAll()
	if len(records) != 3 {
		t.Fatalf("remaining=%d, want 3", len(records))
	}
	for _, r := range records {
		if r.ToolName != "new_tool" {
			t.Errorf("expected new_tool, got %s", r.ToolName)
		}
	}

	// Chain should be valid after purge
	path := filepath.Join(dir, "receipts.jsonl")
	intact, _, depth := VerifyChain(path)
	if !intact {
		t.Error("chain should be intact after purge")
	}
	if depth != 3 {
		t.Errorf("depth=%d, want 3", depth)
	}
}

func TestPurge_AllOld(t *testing.T) {
	dir := t.TempDir()
	l, _ := NewLedger(dir)

	old := time.Now().Add(-72 * time.Hour).UnixMilli()
	for i := 0; i < 3; i++ {
		l.Append(&Record{
			Timestamp:    old,
			ToolName:     "old",
			MutationType: "readonly",
			Outcome:      "success",
		})
	}

	purged, err := l.Purge(24 * time.Hour)
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if purged != 3 {
		t.Errorf("purged=%d, want 3", purged)
	}
	if l.Seq() != 0 {
		t.Errorf("seq=%d, want 0 after full purge", l.Seq())
	}
	if l.LastHash() != "genesis" {
		t.Errorf("lastHash=%q, want genesis", l.LastHash())
	}

	// New append should work from genesis
	l.Append(&Record{
		Timestamp:    time.Now().UnixMilli(),
		ToolName:     "fresh",
		MutationType: "readonly",
		Outcome:      "success",
	})
	if l.Seq() != 1 {
		t.Errorf("seq=%d after fresh append, want 1", l.Seq())
	}
}

func TestPurge_NothingToPurge(t *testing.T) {
	dir := t.TempDir()
	l, _ := NewLedger(dir)

	l.Append(&Record{
		Timestamp:    time.Now().UnixMilli(),
		ToolName:     "recent",
		MutationType: "readonly",
		Outcome:      "success",
	})

	purged, err := l.Purge(24 * time.Hour)
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if purged != 0 {
		t.Errorf("purged=%d, want 0", purged)
	}
}

func TestPurge_NoFile(t *testing.T) {
	dir := t.TempDir()
	l, _ := NewLedger(dir)

	purged, err := l.Purge(24 * time.Hour)
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if purged != 0 {
		t.Errorf("purged=%d, want 0", purged)
	}
}
