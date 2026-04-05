package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/nlink-jp/mcp-guardian/internal/config"
	"github.com/nlink-jp/mcp-guardian/internal/receipt"
)

const splunkHTTPTimeout = 5 * time.Second

// splunkHECEvent is the Splunk HTTP Event Collector event format.
type splunkHECEvent struct {
	Time       float64                `json:"time"`
	Host       string                 `json:"host,omitempty"`
	Source     string                 `json:"source"`
	SourceType string                 `json:"sourcetype"`
	Index      string                 `json:"index,omitempty"`
	Event      map[string]interface{} `json:"event"`
}

// splunkHECExporter sends receipt records to Splunk via HTTP Event Collector.
type splunkHECExporter struct {
	endpoint   string
	token      string
	source     string
	sourceType string
	index      string
	client     *http.Client

	batchSize    int
	batchTimeout time.Duration
	mu           sync.Mutex
	buf          []splunkHECEvent
	timer        *time.Timer
	stopped      bool
}

// NewSplunkHECExporter creates a new Splunk HEC exporter from config.
func NewSplunkHECExporter(cfg *config.Config) *splunkHECExporter {
	batchTimeout := time.Duration(cfg.SplunkHECBatchTimeout) * time.Millisecond
	if batchTimeout <= 0 {
		batchTimeout = 5 * time.Second
	}
	batchSize := cfg.SplunkHECBatchSize
	if batchSize <= 0 {
		batchSize = 10
	}

	e := &splunkHECExporter{
		endpoint:     cfg.SplunkHECEndpoint,
		token:        cfg.SplunkHECToken,
		source:       "mcp-guardian",
		sourceType:   "_json",
		index:        cfg.SplunkHECIndex,
		client:       &http.Client{Timeout: splunkHTTPTimeout},
		batchSize:    batchSize,
		batchTimeout: batchTimeout,
	}
	e.timer = time.AfterFunc(batchTimeout, e.timerFlush)
	return e
}

// Export adds a receipt record to the buffer.
func (e *splunkHECExporter) Export(r *receipt.Record) {
	event := e.convertRecord(r)

	e.mu.Lock()
	if e.stopped {
		e.mu.Unlock()
		return
	}
	e.buf = append(e.buf, event)

	if len(e.buf) >= e.batchSize {
		batch := e.buf
		e.buf = nil
		e.resetTimer()
		e.mu.Unlock()
		go e.send(batch)
		return
	}
	e.mu.Unlock()
}

// Shutdown flushes remaining records and stops the exporter.
func (e *splunkHECExporter) Shutdown() {
	e.mu.Lock()
	e.stopped = true
	e.timer.Stop()
	batch := e.buf
	e.buf = nil
	e.mu.Unlock()

	if len(batch) > 0 {
		e.send(batch)
	}
}

func (e *splunkHECExporter) convertRecord(r *receipt.Record) splunkHECEvent {
	event := map[string]interface{}{
		"seq":           r.Seq,
		"hash":          r.Hash,
		"toolName":      r.ToolName,
		"target":        r.Target,
		"mutationType":  r.MutationType,
		"outcome":       r.Outcome,
		"durationMs":    r.DurationMs,
		"title":         r.Title,
		"summary":       r.Summary,
	}
	if r.ErrorText != "" {
		event["errorText"] = r.ErrorText
	}
	if r.ConvergenceSignal != "" {
		event["convergenceSignal"] = r.ConvergenceSignal
	}

	return splunkHECEvent{
		Time:       float64(r.Timestamp) / 1000.0, // ms to epoch seconds
		Source:     e.source,
		SourceType: e.sourceType,
		Index:      e.index,
		Event:      event,
	}
}

func (e *splunkHECExporter) timerFlush() {
	e.mu.Lock()
	if e.stopped || len(e.buf) == 0 {
		e.mu.Unlock()
		return
	}
	batch := e.buf
	e.buf = nil
	if !e.stopped {
		e.resetTimer()
	}
	e.mu.Unlock()

	go e.send(batch)
}

func (e *splunkHECExporter) resetTimer() {
	e.timer.Reset(e.batchTimeout)
}

func (e *splunkHECExporter) send(events []splunkHECEvent) {
	// Splunk HEC accepts multiple events as concatenated JSON objects (not an array)
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			fmt.Fprintf(os.Stderr, "mcp-guardian: splunk-hec: marshal error: %v\n", err)
			return
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), splunkHTTPTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, &buf)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcp-guardian: splunk-hec: request error: %v\n", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Splunk "+e.token)

	resp, err := e.client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcp-guardian: splunk-hec: send error: %v\n", err)
		return
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		fmt.Fprintf(os.Stderr, "mcp-guardian: splunk-hec: HTTP %d\n", resp.StatusCode)
	}
}
