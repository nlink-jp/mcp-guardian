package receipt

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// receiptsPrefix is the filename prefix for per-process receipt files.
// Each process writes to receipts-<unixtime>-<pid>.jsonl to avoid
// concurrent write conflicts without file locking.
const receiptsPrefix = "receipts-"

// Ledger manages the append-only hash-chained receipt log.
// Each process instance writes to its own file identified by startup
// timestamp and PID, ensuring parallel safety without locks.
type Ledger struct {
	dir      string
	file     string // basename of this process's receipt file
	seq      int
	lastHash string
}

// NewLedger creates a ledger with a process-specific receipt file.
// The file is named receipts-<unixtime>-<pid>.jsonl.
// If the file already exists (e.g., after a rapid restart with PID reuse),
// it recovers state from the last record.
func NewLedger(dir string) (*Ledger, error) {
	file := fmt.Sprintf("receipts-%d-%d.jsonl", time.Now().UnixMilli(), os.Getpid())
	l := &Ledger{dir: dir, file: file, lastHash: "genesis"}

	path := filepath.Join(dir, file)
	last, err := readLastRecord(path)
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
// In-memory state is updated only after a successful disk write so that
// governance_status never reports receipts that were not actually persisted.
func (l *Ledger) Append(r *Record) error {
	nextSeq := l.seq + 1
	r.Seq = nextSeq
	r.PreviousHash = l.lastHash
	r.Hash = ComputeHash(r, l.lastHash)

	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal receipt: %w", err)
	}

	path := filepath.Join(l.dir, l.file)
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

	// Commit in-memory state only after successful disk write.
	l.seq = nextSeq
	l.lastHash = r.Hash
	return nil
}

// Purge removes receipts older than maxAge from ALL receipt files in the
// state directory. Empty files are deleted. The current process's file is
// re-chained in place. Returns the total number of records purged.
func (l *Ledger) Purge(maxAge time.Duration) (int, error) {
	files, err := listReceiptFiles(l.dir)
	if err != nil {
		return 0, err
	}

	cutoff := time.Now().Add(-maxAge).UnixMilli()
	totalPurged := 0

	for _, name := range files {
		path := filepath.Join(l.dir, name)
		purged, err := purgeFile(path, cutoff)
		if err != nil {
			return totalPurged, fmt.Errorf("purge %s: %w", name, err)
		}
		totalPurged += purged
	}

	// Refresh in-memory state from our own file
	myPath := filepath.Join(l.dir, l.file)
	last, err := readLastRecord(myPath)
	if err != nil {
		if os.IsNotExist(err) {
			l.seq = 0
			l.lastHash = "genesis"
			return totalPurged, nil
		}
		return totalPurged, err
	}
	if last != nil {
		l.seq = last.Seq
		l.lastHash = last.Hash
	} else {
		l.seq = 0
		l.lastHash = "genesis"
	}

	return totalPurged, nil
}

// LoadAll reads all records from the current process's receipt file.
func (l *Ledger) LoadAll() ([]*Record, error) {
	path := filepath.Join(l.dir, l.file)
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

// File returns the basename of this ledger's receipt file.
func (l *Ledger) File() string {
	return l.file
}

// LoadReceipts reads all records from a single receipt file path.
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

// LoadAllReceipts reads records from all receipt files in the directory,
// merged and sorted by timestamp. All files are read even if some fail;
// errors are collected and returned alongside any successfully loaded records.
func LoadAllReceipts(dir string) ([]*Record, error) {
	files, err := listReceiptFiles(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var all []*Record
	var errs []string
	for _, name := range files {
		records, err := LoadReceipts(filepath.Join(dir, name))
		if err != nil {
			if !os.IsNotExist(err) {
				errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			}
			continue
		}
		all = append(all, records...)
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].Timestamp < all[j].Timestamp
	})

	if len(errs) > 0 {
		return all, fmt.Errorf("errors loading receipts: %s", strings.Join(errs, "; "))
	}
	return all, nil
}

// HasReceipts returns true if any receipt files exist in the directory.
func HasReceipts(dir string) bool {
	files, err := listReceiptFiles(dir)
	return err == nil && len(files) > 0
}

// VerifyChain checks the integrity of hash chains across all receipt files.
// Each file is verified independently (each starts from "genesis").
// Returns (intact, brokenFile, brokenAtSeq, totalDepth).
func VerifyChain(dir string) (bool, string, int, int) {
	files, err := listReceiptFiles(dir)
	if err != nil || len(files) == 0 {
		return true, "", 0, 0
	}

	totalDepth := 0
	for _, name := range files {
		path := filepath.Join(dir, name)
		intact, brokenAt, depth := verifyFile(path)
		totalDepth += depth
		if !intact {
			return false, name, brokenAt, totalDepth
		}
	}
	return true, "", 0, totalDepth
}

// verifyFile checks the hash chain integrity of a single receipt file.
func verifyFile(path string) (bool, int, int) {
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

// listReceiptFiles returns the basenames of all receipt files in the directory,
// sorted alphabetically. Matches both legacy "receipts.jsonl" and new
// "receipts-<unixtime>-<pid>.jsonl" patterns.
func listReceiptFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "receipts.jsonl" ||
			(strings.HasPrefix(name, receiptsPrefix) && strings.HasSuffix(name, ".jsonl")) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// purgeFile removes records older than cutoff from a single receipt file.
// Remaining records are re-chained from "genesis".
// Returns the number of purged records.
func purgeFile(path string, cutoffMs int64) (int, error) {
	records, err := LoadReceipts(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	// Find first record to keep
	keepFrom := 0
	for i, r := range records {
		if r.Timestamp >= cutoffMs {
			keepFrom = i
			break
		}
		if i == len(records)-1 {
			keepFrom = len(records)
		}
	}

	purged := keepFrom
	if purged == 0 {
		return 0, nil
	}

	kept := records[keepFrom:]

	// If all records are old, delete the file entirely.
	if len(kept) == 0 {
		os.Remove(path)
		return purged, nil
	}

	// Re-chain remaining records
	prevHash := "genesis"
	for _, r := range kept {
		r.PreviousHash = prevHash
		r.Hash = ComputeHash(r, prevHash)
		prevHash = r.Hash
	}

	// Write atomically
	tmpPath := path + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return 0, fmt.Errorf("create temp: %w", err)
	}

	for _, r := range kept {
		data, err := json.Marshal(r)
		if err != nil {
			f.Close()
			os.Remove(tmpPath)
			return 0, fmt.Errorf("marshal: %w", err)
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			f.Close()
			os.Remove(tmpPath)
			return 0, fmt.Errorf("write: %w", err)
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return 0, fmt.Errorf("sync: %w", err)
	}
	f.Close()

	if err := os.Rename(tmpPath, path); err != nil {
		return 0, fmt.Errorf("rename: %w", err)
	}

	return purged, nil
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

	for offset > 0 && len(buf) < maxScanSize {
		readSize := min(int64(4096), offset)
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
