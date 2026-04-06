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

	// Verify chain (directory-level)
	intact, _, _, depth := VerifyChain(dir)
	if !intact {
		t.Fatal("chain broken")
	}
	if depth != 3 {
		t.Fatalf("expected depth 3, got %d", depth)
	}

	// Tamper with a record and verify again
	path := filepath.Join(dir, ledger.File())
	records, _ := LoadReceipts(path)
	records[1].Hash = "tampered"
	f, _ := os.Create(path)
	for _, r := range records {
		data, _ := json.Marshal(r)
		f.Write(append(data, '\n'))
	}
	f.Close()

	intact, brokenFile, brokenAt, _ := VerifyChain(dir)
	if intact {
		t.Fatal("expected chain to be broken after tampering")
	}
	if brokenFile != ledger.File() {
		t.Fatalf("expected broken file %q, got %q", ledger.File(), brokenFile)
	}
	if brokenAt != 2 {
		t.Fatalf("expected break at seq 2, got %d", brokenAt)
	}
}

func TestReadLastRecord_Recovery(t *testing.T) {
	dir := t.TempDir()

	// Create a ledger and append records to build a valid chain
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

	// Directly test readLastRecord on the file
	path := filepath.Join(dir, l1.File())
	last, err := readLastRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	if last == nil {
		t.Fatal("expected non-nil last record")
	}
	if last.Seq != lastSeq {
		t.Errorf("seq=%d, want %d", last.Seq, lastSeq)
	}
	if last.Hash != lastHash {
		t.Errorf("hash=%q, want %q", last.Hash, lastHash)
	}
}

func TestNewLedger_FreshStart(t *testing.T) {
	dir := t.TempDir()

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
	intact, _, _, depth := VerifyChain(dir)
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

func TestAppend_FailedWriteDoesNotUpdateState(t *testing.T) {
	dir := t.TempDir()
	l, _ := NewLedger(dir)

	// Append one record successfully
	r1 := &Record{
		Timestamp:    1700000000000,
		ToolName:     "test",
		MutationType: "readonly",
		Outcome:      "success",
	}
	if err := l.Append(r1); err != nil {
		t.Fatal(err)
	}
	seqBefore := l.Seq()
	hashBefore := l.LastHash()

	// Make the receipts file unwritable to force a write failure
	path := filepath.Join(dir, l.File())
	os.Chmod(path, 0400)
	defer os.Chmod(path, 0600)

	r2 := &Record{
		Timestamp:    1700000001000,
		ToolName:     "test2",
		MutationType: "readonly",
		Outcome:      "success",
	}
	err := l.Append(r2)
	if err == nil {
		t.Fatal("expected Append to fail on read-only file")
	}

	if l.Seq() != seqBefore {
		t.Errorf("seq changed after failed write: got %d, want %d", l.Seq(), seqBefore)
	}
	if l.LastHash() != hashBefore {
		t.Errorf("lastHash changed after failed write: got %q, want %q", l.LastHash(), hashBefore)
	}

	// Restore and verify recovery
	os.Chmod(path, 0600)
	r3 := &Record{
		Timestamp:    1700000002000,
		ToolName:     "test3",
		MutationType: "readonly",
		Outcome:      "success",
	}
	if err := l.Append(r3); err != nil {
		t.Fatalf("append after recovery: %v", err)
	}
	if l.Seq() != seqBefore+1 {
		t.Errorf("seq after recovery: got %d, want %d", l.Seq(), seqBefore+1)
	}

	intact, _, _, depth := VerifyChain(dir)
	if !intact {
		t.Error("chain should be intact after recovery")
	}
	if depth != 2 {
		t.Errorf("depth=%d, want 2", depth)
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

func TestLoadAllReceipts_MultipleFiles(t *testing.T) {
	dir := t.TempDir()

	// Simulate two processes by writing to separate files
	file1 := "receipts-1000000-100.jsonl"
	file2 := "receipts-1000000-200.jsonl"

	writeRecords(t, dir, file1, []*Record{
		{Seq: 1, Timestamp: 1700000001000, ToolName: "tool_a", Outcome: "success", PreviousHash: "genesis"},
		{Seq: 2, Timestamp: 1700000003000, ToolName: "tool_a", Outcome: "success"},
	})
	writeRecords(t, dir, file2, []*Record{
		{Seq: 1, Timestamp: 1700000002000, ToolName: "tool_b", Outcome: "success", PreviousHash: "genesis"},
		{Seq: 2, Timestamp: 1700000004000, ToolName: "tool_b", Outcome: "success"},
	})

	records, err := LoadAllReceipts(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 4 {
		t.Fatalf("expected 4 records, got %d", len(records))
	}

	// Should be sorted by timestamp
	if records[0].ToolName != "tool_a" || records[0].Timestamp != 1700000001000 {
		t.Errorf("record[0]: expected tool_a@1000, got %s@%d", records[0].ToolName, records[0].Timestamp)
	}
	if records[1].ToolName != "tool_b" || records[1].Timestamp != 1700000002000 {
		t.Errorf("record[1]: expected tool_b@2000, got %s@%d", records[1].ToolName, records[1].Timestamp)
	}
}

func TestHasReceipts(t *testing.T) {
	dir := t.TempDir()

	if HasReceipts(dir) {
		t.Error("expected no receipts in empty dir")
	}

	l, _ := NewLedger(dir)
	l.Append(&Record{
		Timestamp:    time.Now().UnixMilli(),
		ToolName:     "test",
		MutationType: "readonly",
		Outcome:      "success",
	})

	if !HasReceipts(dir) {
		t.Error("expected receipts after append")
	}
}

func TestLegacyReceiptsFile(t *testing.T) {
	dir := t.TempDir()

	// Write a legacy receipts.jsonl file
	writeRecords(t, dir, "receipts.jsonl", []*Record{
		{Seq: 1, Timestamp: 1700000001000, ToolName: "legacy", Outcome: "success", PreviousHash: "genesis"},
	})

	records, err := LoadAllReceipts(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record from legacy file, got %d", len(records))
	}
	if records[0].ToolName != "legacy" {
		t.Errorf("expected legacy tool, got %s", records[0].ToolName)
	}

	if !HasReceipts(dir) {
		t.Error("HasReceipts should detect legacy file")
	}
}

func TestPurge_DeletesEmptyForeignFiles(t *testing.T) {
	dir := t.TempDir()
	l, _ := NewLedger(dir)

	// Create a foreign process file with only old records
	foreignFile := "receipts-999999-999.jsonl"
	old := time.Now().Add(-72 * time.Hour).UnixMilli()
	r := &Record{
		Seq: 1, Timestamp: old, ToolName: "foreign", MutationType: "readonly",
		Outcome: "success", PreviousHash: "genesis",
	}
	r.Hash = ComputeHash(r, "genesis")
	writeHashedRecords(t, dir, foreignFile, []*Record{r})

	// Also add a recent record to our file
	l.Append(&Record{
		Timestamp:    time.Now().UnixMilli(),
		ToolName:     "ours",
		MutationType: "readonly",
		Outcome:      "success",
	})

	purged, err := l.Purge(24 * time.Hour)
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if purged != 1 {
		t.Errorf("purged=%d, want 1", purged)
	}

	// Foreign file should be deleted (became empty after purge)
	if _, err := os.Stat(filepath.Join(dir, foreignFile)); !os.IsNotExist(err) {
		t.Error("expected foreign file to be deleted after purge")
	}

	// Our file should still exist
	if _, err := os.Stat(filepath.Join(dir, l.File())); err != nil {
		t.Error("expected our file to still exist")
	}
}

// writeRecords writes records to a file without hash chaining (for testing reads).
func writeRecords(t *testing.T, dir, filename string, records []*Record) {
	t.Helper()
	f, err := os.Create(filepath.Join(dir, filename))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, r := range records {
		data, _ := json.Marshal(r)
		f.Write(append(data, '\n'))
	}
}

// writeHashedRecords writes records with pre-computed hashes.
func writeHashedRecords(t *testing.T, dir, filename string, records []*Record) {
	t.Helper()
	f, err := os.Create(filepath.Join(dir, filename))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, r := range records {
		data, _ := json.Marshal(r)
		f.Write(append(data, '\n'))
	}
}
