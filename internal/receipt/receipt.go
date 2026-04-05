package receipt

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const receiptsFile = "receipts.jsonl"

// Ledger manages the append-only hash-chained receipt log.
type Ledger struct {
	dir      string
	seq      int
	lastHash string
}

// NewLedger creates a ledger, loading the current chain state from disk.
// Only reads the last line of the receipt file to recover seq and lastHash,
// avoiding a full file scan on startup.
func NewLedger(dir string) (*Ledger, error) {
	l := &Ledger{dir: dir, lastHash: "genesis"}

	last, err := readLastRecord(filepath.Join(dir, receiptsFile))
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "mcp-guardian: warning: failed to read last receipt: %v (starting fresh)\n", err)
		}
		return l, nil // fresh ledger
	}
	if last != nil {
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
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open receipts: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write receipt: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync receipt: %w", err)
	}
	return nil
}

// Purge removes receipts older than maxAge from the receipt file.
// Remaining records are re-chained starting from "genesis".
// Returns the number of records purged.
func (l *Ledger) Purge(maxAge time.Duration) (int, error) {
	path := filepath.Join(l.dir, receiptsFile)
	records, err := LoadReceipts(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	cutoff := time.Now().Add(-maxAge).UnixMilli()

	// Find first record to keep
	keepFrom := 0
	for i, r := range records {
		if r.Timestamp >= cutoff {
			keepFrom = i
			break
		}
		if i == len(records)-1 {
			// All records are old
			keepFrom = len(records)
		}
	}

	purged := keepFrom
	if purged == 0 {
		return 0, nil // nothing to purge
	}

	kept := records[keepFrom:]

	// Re-chain the remaining records
	prevHash := "genesis"
	for _, r := range kept {
		r.PreviousHash = prevHash
		r.Hash = ComputeHash(r, prevHash)
		prevHash = r.Hash
	}

	// Write surviving records atomically
	tmpPath := path + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return 0, fmt.Errorf("create temp receipts: %w", err)
	}

	for _, r := range kept {
		data, err := json.Marshal(r)
		if err != nil {
			f.Close()
			os.Remove(tmpPath)
			return 0, fmt.Errorf("marshal receipt: %w", err)
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			f.Close()
			os.Remove(tmpPath)
			return 0, fmt.Errorf("write receipt: %w", err)
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return 0, fmt.Errorf("sync receipts: %w", err)
	}
	f.Close()

	if err := os.Rename(tmpPath, path); err != nil {
		return 0, fmt.Errorf("rename receipts: %w", err)
	}

	// Update ledger state
	if len(kept) > 0 {
		last := kept[len(kept)-1]
		l.seq = last.Seq
		l.lastHash = last.Hash
	} else {
		l.seq = 0
		l.lastHash = "genesis"
	}

	return purged, nil
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

// readLastRecord reads only the last JSON line from a receipt file.
// This avoids loading the entire file into memory on startup.
func readLastRecord(path string) (*Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() == 0 {
		return nil, nil
	}

	// Read backwards from EOF to find the last newline before the final line.
	// Cap at 64KB to prevent unbounded memory on corrupted files.
	const maxScanSize = 64 * 1024
	buf := make([]byte, 0, 4096)
	offset := info.Size()
	found := false

	for offset > 0 && !found && len(buf) < maxScanSize {
		readSize := int64(4096)
		if readSize > offset {
			readSize = offset
		}
		offset -= readSize

		chunk := make([]byte, readSize)
		if _, err := f.ReadAt(chunk, offset); err != nil && err != io.EOF {
			return nil, err
		}

		// Prepend chunk to buf
		buf = append(chunk, buf...)

		// Look for the second-to-last newline (last line boundary)
		// Skip trailing newline if present
		end := len(buf) - 1
		for end >= 0 && buf[end] == '\n' {
			end--
		}
		for i := end; i >= 0; i-- {
			if buf[i] == '\n' {
				// Found the start of the last line
				line := buf[i+1 : end+1]
				var r Record
				if err := json.Unmarshal(line, &r); err != nil {
					return nil, err
				}
				return &r, nil
			}
		}
	}

	// No newline found — file is a single line
	end := len(buf) - 1
	for end >= 0 && buf[end] == '\n' {
		end--
	}
	if end < 0 {
		return nil, nil
	}

	var r Record
	if err := json.Unmarshal(buf[:end+1], &r); err != nil {
		return nil, err
	}
	return &r, nil
}
