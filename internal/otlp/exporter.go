package otlp

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

	"github.com/nlink-jp/mcp-guardian/internal/receipt"
)

const httpTimeout = 5 * time.Second

// ExporterConfig holds configuration for the OTLP exporter.
type ExporterConfig struct {
	Endpoint     string
	Headers      map[string]string
	Resource     Resource
	Scope        InstrumentationScope
	TraceID      string
	BatchSize    int
	BatchTimeout time.Duration
}

// Exporter batches receipt records and exports them as OTLP Logs and Traces.
type Exporter struct {
	endpoint string
	headers  map[string]string
	resource Resource
	scope    InstrumentationScope
	traceID  string

	batchSize int
	mu        sync.Mutex
	logBuf    []LogRecord
	spanBuf   []Span
	timer     *time.Timer
	stopped   bool
}

// NewExporter creates a new OTLP batch exporter.
func NewExporter(cfg ExporterConfig) *Exporter {
	e := &Exporter{
		endpoint:  cfg.Endpoint,
		headers:   cfg.Headers,
		resource:  cfg.Resource,
		scope:     cfg.Scope,
		traceID:   cfg.TraceID,
		batchSize: cfg.BatchSize,
	}
	if e.batchSize <= 0 {
		e.batchSize = 10
	}
	e.timer = time.AfterFunc(cfg.BatchTimeout, e.timerFlush)
	return e
}

// Export adds a receipt record to the export buffer.
// Flushes if batch size is reached.
func (e *Exporter) Export(r *receipt.Record) {
	logRec := ConvertToLogRecord(r)
	span := ConvertToSpan(r, e.traceID)

	e.mu.Lock()
	if e.stopped {
		e.mu.Unlock()
		return
	}
	e.logBuf = append(e.logBuf, logRec)
	e.spanBuf = append(e.spanBuf, span)

	if len(e.logBuf) >= e.batchSize {
		logs := e.logBuf
		spans := e.spanBuf
		e.logBuf = nil
		e.spanBuf = nil
		e.resetTimer()
		e.mu.Unlock()
		e.send(logs, spans)
		return
	}
	e.mu.Unlock()
}

// Shutdown flushes remaining records and stops the exporter.
func (e *Exporter) Shutdown() {
	e.mu.Lock()
	e.stopped = true
	e.timer.Stop()
	logs := e.logBuf
	spans := e.spanBuf
	e.logBuf = nil
	e.spanBuf = nil
	e.mu.Unlock()

	if len(logs) > 0 {
		e.send(logs, spans)
	}
}

func (e *Exporter) timerFlush() {
	e.mu.Lock()
	if e.stopped || len(e.logBuf) == 0 {
		e.mu.Unlock()
		return
	}
	logs := e.logBuf
	spans := e.spanBuf
	e.logBuf = nil
	e.spanBuf = nil
	e.mu.Unlock()

	e.send(logs, spans)

	e.mu.Lock()
	if !e.stopped {
		e.resetTimer()
	}
	e.mu.Unlock()
}

func (e *Exporter) resetTimer() {
	e.timer.Reset(httpTimeout)
}

func (e *Exporter) send(logs []LogRecord, spans []Span) {
	// Send logs
	logsPayload := LogsPayload{
		ResourceLogs: []ResourceLogs{{
			Resource: e.resource,
			ScopeLogs: []ScopeLogs{{
				Scope:      e.scope,
				LogRecords: logs,
			}},
		}},
	}
	e.post("/v1/logs", logsPayload)

	// Send traces
	tracesPayload := TracesPayload{
		ResourceSpans: []ResourceSpans{{
			Resource: e.resource,
			ScopeSpans: []ScopeSpans{{
				Scope: e.scope,
				Spans: spans,
			}},
		}},
	}
	e.post("/v1/traces", tracesPayload)
}

func (e *Exporter) post(path string, payload interface{}) {
	body, err := json.Marshal(payload)
	if err != nil {
		logErr("otlp: marshal error: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), httpTimeout)
	defer cancel()

	url := e.endpoint + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		logErr("otlp: request error: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range e.headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logErr("otlp: send error (%s): %v", path, err)
		return
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		logErr("otlp: %s returned %d", path, resp.StatusCode)
	}
}

func logErr(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "mcp-guardian: "+format+"\n", args...)
}
