package otlp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/nlink-jp/mcp-guardian/internal/receipt"
)

func TestConvertToLogRecord_Success(t *testing.T) {
	r := &receipt.Record{
		Seq:          1,
		Hash:         "abc123def456abc123def456abc123de",
		Timestamp:    1700000000000,
		ToolName:     "echo_tool",
		Target:       "/tmp/test",
		MutationType: "read-only",
		Outcome:      "success",
		DurationMs:   45,
		Title:        "echo_tool on /tmp/test",
		Summary:      "OK (45ms)",
	}

	lr := ConvertToLogRecord(r)

	if lr.SeverityNumber != SeverityINFO {
		t.Errorf("expected severity INFO (%d), got %d", SeverityINFO, lr.SeverityNumber)
	}
	if lr.SeverityText != "INFO" {
		t.Errorf("expected severity text INFO, got %s", lr.SeverityText)
	}
	if *lr.Body.StringValue != "echo_tool on /tmp/test" {
		t.Errorf("unexpected body: %s", *lr.Body.StringValue)
	}
	if lr.TimeUnixNano != "1700000000000000000" {
		t.Errorf("unexpected timeUnixNano: %s", lr.TimeUnixNano)
	}

	assertAttr(t, lr.Attributes, "mcp.tool.name", "echo_tool")
	assertAttr(t, lr.Attributes, "mcp.outcome", "success")
}

func TestConvertToLogRecord_Blocked(t *testing.T) {
	r := &receipt.Record{
		Seq:      2,
		Hash:     "def456",
		Outcome:  "blocked",
		Title:    "write_file blocked",
		ToolName: "write_file",
	}

	lr := ConvertToLogRecord(r)

	if lr.SeverityNumber != SeverityWARN {
		t.Errorf("blocked should be WARN severity, got %d", lr.SeverityNumber)
	}
}

func TestConvertToSpan(t *testing.T) {
	r := &receipt.Record{
		Seq:          1,
		Hash:         "abc123def456abc123def456abc123de",
		Timestamp:    1700000000000,
		ToolName:     "echo_tool",
		Target:       "/tmp/test",
		MutationType: "read-only",
		Outcome:      "success",
		DurationMs:   100,
		Title:        "echo_tool on /tmp/test",
		Summary:      "OK (100ms)",
	}

	traceID := "0123456789abcdef0123456789abcdef"
	span := ConvertToSpan(r, traceID)

	if span.TraceID != traceID {
		t.Errorf("unexpected traceID: %s", span.TraceID)
	}
	if span.Name != "echo_tool" {
		t.Errorf("unexpected span name: %s", span.Name)
	}
	if span.Kind != SpanKindClient {
		t.Errorf("expected kind %d, got %d", SpanKindClient, span.Kind)
	}
	if span.StartTimeUnixNano != "1700000000000000000" {
		t.Errorf("unexpected start time: %s", span.StartTimeUnixNano)
	}
	if span.EndTimeUnixNano != "1700000000100000000" {
		t.Errorf("unexpected end time: %s (expected 1700000000100000000)", span.EndTimeUnixNano)
	}
	if span.Status.Code != StatusCodeOK {
		t.Errorf("expected status OK, got %d", span.Status.Code)
	}
	if span.SpanID != "abc123def456abc1" {
		t.Errorf("unexpected spanID: %s", span.SpanID)
	}
}

func TestConvertToSpan_Error(t *testing.T) {
	r := &receipt.Record{
		Seq:      1,
		Hash:     "abc123def456abc123def456",
		Outcome:  "error",
		ToolName: "fail_tool",
		Summary:  "Failed: permission denied",
	}

	span := ConvertToSpan(r, "trace1234")
	if span.Status.Code != StatusCodeError {
		t.Errorf("expected status ERROR, got %d", span.Status.Code)
	}
}

func TestDeriveTraceID(t *testing.T) {
	id1 := DeriveTraceID("controller-1", 1)
	id2 := DeriveTraceID("controller-1", 2)
	id3 := DeriveTraceID("controller-1", 1)

	if len(id1) != 32 {
		t.Errorf("traceID should be 32 hex chars, got %d", len(id1))
	}
	if id1 == id2 {
		t.Error("different epochs should produce different trace IDs")
	}
	if id1 != id3 {
		t.Error("same inputs should produce same trace ID")
	}
}

func TestExporter_BatchFlush(t *testing.T) {
	var mu sync.Mutex
	var logPosts, tracePosts int
	var lastLogsBody, lastTracesBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/v1/logs":
			logPosts++
			lastLogsBody = body
		case "/v1/traces":
			tracePosts++
			lastTracesBody = body
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	e := NewExporter(ExporterConfig{
		Endpoint:     srv.URL,
		Resource:     Resource{Attributes: []KeyValue{{Key: "service.name", Value: StringVal("test")}}},
		Scope:        InstrumentationScope{Name: "test"},
		TraceID:      "deadbeef" + "deadbeef" + "deadbeef" + "deadbeef",
		BatchSize:    3,
		BatchTimeout: 10 * time.Second,
	})

	// Export 3 records — should trigger batch flush
	for i := 0; i < 3; i++ {
		e.Export(&receipt.Record{
			Seq:        i + 1,
			Hash:       "abc123def456abc1abc123def456abc1",
			Timestamp:  1700000000000 + int64(i*1000),
			ToolName:   "echo_tool",
			Outcome:    "success",
			DurationMs: 10,
			Title:      "test",
			Summary:    "OK",
		})
	}

	// Give goroutine time to complete
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if logPosts != 1 {
		t.Errorf("expected 1 log POST, got %d", logPosts)
	}
	if tracePosts != 1 {
		t.Errorf("expected 1 traces POST, got %d", tracePosts)
	}

	// Verify logs payload structure
	var logsPayload LogsPayload
	if err := json.Unmarshal(lastLogsBody, &logsPayload); err != nil {
		t.Fatalf("invalid logs JSON: %v", err)
	}
	if len(logsPayload.ResourceLogs) != 1 {
		t.Fatal("expected 1 resourceLogs")
	}
	records := logsPayload.ResourceLogs[0].ScopeLogs[0].LogRecords
	if len(records) != 3 {
		t.Errorf("expected 3 log records, got %d", len(records))
	}

	// Verify traces payload structure
	var tracesPayload TracesPayload
	if err := json.Unmarshal(lastTracesBody, &tracesPayload); err != nil {
		t.Fatalf("invalid traces JSON: %v", err)
	}
	spans := tracesPayload.ResourceSpans[0].ScopeSpans[0].Spans
	if len(spans) != 3 {
		t.Errorf("expected 3 spans, got %d", len(spans))
	}

	e.Shutdown()
}

func TestExporter_ShutdownFlush(t *testing.T) {
	var mu sync.Mutex
	var logPosts int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/v1/logs" {
			logPosts++
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	e := NewExporter(ExporterConfig{
		Endpoint:     srv.URL,
		Resource:     Resource{},
		Scope:        InstrumentationScope{Name: "test"},
		TraceID:      "deadbeefdeadbeefdeadbeefdeadbeef",
		BatchSize:    100, // large batch — won't trigger by size
		BatchTimeout: 10 * time.Second,
	})

	// Export 2 records (below batch size)
	for i := 0; i < 2; i++ {
		e.Export(&receipt.Record{
			Seq:      i + 1,
			Hash:     "abc123def456abc1abc123def456abc1",
			ToolName: "echo_tool",
			Outcome:  "success",
			Title:    "test",
		})
	}

	// No flush yet
	mu.Lock()
	prePosts := logPosts
	mu.Unlock()
	if prePosts != 0 {
		t.Errorf("should not have flushed yet, got %d posts", prePosts)
	}

	// Shutdown should flush
	e.Shutdown()

	mu.Lock()
	defer mu.Unlock()
	if logPosts != 1 {
		t.Errorf("shutdown should flush remaining records, got %d posts", logPosts)
	}
}

func TestExporter_CustomHeaders(t *testing.T) {
	var receivedAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/logs" {
			receivedAuth = r.Header.Get("Authorization")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	e := NewExporter(ExporterConfig{
		Endpoint:     srv.URL,
		Headers:      map[string]string{"Authorization": "Bearer test-token"},
		Resource:     Resource{},
		Scope:        InstrumentationScope{Name: "test"},
		TraceID:      "deadbeefdeadbeefdeadbeefdeadbeef",
		BatchSize:    1,
		BatchTimeout: 10 * time.Second,
	})

	e.Export(&receipt.Record{
		Seq:      1,
		Hash:     "abc123def456abc1abc123def456abc1",
		ToolName: "echo_tool",
		Outcome:  "success",
		Title:    "test",
	})

	time.Sleep(200 * time.Millisecond)
	e.Shutdown()

	if receivedAuth != "Bearer test-token" {
		t.Errorf("expected auth header 'Bearer test-token', got '%s'", receivedAuth)
	}
}

func TestIntToString(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "0"},
		{1, "1"},
		{-1, "-1"},
		{1700000000000000000, "1700000000000000000"},
		{42, "42"},
	}
	for _, tt := range tests {
		got := intToString(tt.input)
		if got != tt.expected {
			t.Errorf("intToString(%d) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func assertAttr(t *testing.T, attrs []KeyValue, key, expected string) {
	t.Helper()
	for _, a := range attrs {
		if a.Key == key {
			if a.Value.StringValue == nil || *a.Value.StringValue != expected {
				t.Errorf("attr %s: expected %q, got %v", key, expected, a.Value)
			}
			return
		}
	}
	t.Errorf("attr %s not found", key)
}
