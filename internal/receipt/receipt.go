package receipt

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const receiptsFile = "receipts.jsonl"

// Ledger manages the append-only hash-chained receipt log.
type Ledger struct {
	dir      string
	seq      int
	lastHash string
}

// NewLedger creates a ledger, loading the current chain state from disk.
func NewLedger(dir string) (*Ledger, error) {
	l := &Ledger{dir: dir, lastHash: "genesis"}

	records, err := l.LoadAll()
	if err != nil {
		return l, nil // fresh ledger
	}
	if len(records) > 0 {
		last := records[len(records)-1]
		l.seq = last.Seq
		l.lastHash = last.Hash
	}
	return l, nil
}

// Append adds a new record to the ledger, computing its hash and writing to disk.
func (l *Ledger) Append(r *Record) error {
	l.seq++
	r.Seq = l.seq
	r.PreviousHash = l.lastHash
	r.Hash = ComputeHash(r, l.lastHash)
	l.lastHash = r.Hash

	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal receipt: %w", err)
	}

	path := filepath.Join(l.dir, receiptsFile)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open receipts: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write receipt: %w", err)
	}
	return nil
}

// LoadAll reads all records from the receipt file.
func (l *Ledger) LoadAll() ([]*Record, error) {
	path := filepath.Join(l.dir, receiptsFile)
	return LoadReceipts(path)
}

// LastHash returns the current chain head hash.
func (l *Ledger) LastHash() string {
	return l.lastHash
}

// Seq returns the current sequence number.
func (l *Ledger) Seq() int {
	return l.seq
}

// LoadReceipts reads all records from a receipt file path.
func LoadReceipts(path string) ([]*Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var records []*Record
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var r Record
		if err := json.Unmarshal(line, &r); err != nil {
			return records, fmt.Errorf("parse receipt line: %w", err)
		}
		records = append(records, &r)
	}
	return records, scanner.Err()
}

// VerifyChain checks the integrity of the hash chain.
// Returns (intact, brokenAtSeq, totalDepth).
func VerifyChain(path string) (bool, int, int) {
	records, err := LoadReceipts(path)
	if err != nil || len(records) == 0 {
		return true, 0, 0
	}

	prevHash := "genesis"
	for _, r := range records {
		expected := ComputeHash(r, prevHash)
		if r.Hash != expected {
			return false, r.Seq, len(records)
		}
		if r.PreviousHash != prevHash {
			return false, r.Seq, len(records)
		}
		prevHash = r.Hash
	}
	return true, 0, len(records)
}
