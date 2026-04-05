package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/nlink-jp/mcp-guardian/internal/config"
	"github.com/nlink-jp/mcp-guardian/internal/receipt"
)

func TestSplunkHECExporter_SendsEvents(t *testing.T) {
	var mu sync.Mutex
	var received []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Splunk test-token" {
			t.Errorf("Authorization=%q, want Splunk test-token", r.Header.Get("Authorization"))
		}
		mu.Lock()
		body, _ := io.ReadAll(r.Body)
		received = append(received, body...)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{
		SplunkHECEndpoint:     srv.URL,
		SplunkHECToken:        "test-token",
		SplunkHECIndex:        "mcp-audit",
		SplunkHECBatchSize:    2,
		SplunkHECBatchTimeout: 5000,
	}

	e := NewSplunkHECExporter(cfg)

	// Send 2 records to trigger batch flush
	for i := 0; i < 2; i++ {
		e.Export(&receipt.Record{
			Seq:          i + 1,
			Timestamp:    time.Now().UnixMilli(),
			ToolName:     "test_tool",
			Target:       "/tmp/test",
			MutationType: "readonly",
			Outcome:      "success",
			DurationMs:   10,
			Title:        "test on /tmp/test",
			Summary:      "OK (10ms)",
		})
	}

	// Wait for async send
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	data := received
	mu.Unlock()

	if len(data) == 0 {
		t.Fatal("no data received by Splunk HEC server")
	}

	// Parse concatenated JSON events
	decoder := json.NewDecoder(jsonReader(data))
	var events []splunkHECEvent
	for decoder.More() {
		var ev splunkHECEvent
		if err := decoder.Decode(&ev); err != nil {
			t.Fatalf("decode event: %v", err)
		}
		events = append(events, ev)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	if events[0].Source != "mcp-guardian" {
		t.Errorf("source=%q, want mcp-guardian", events[0].Source)
	}
	if events[0].Index != "mcp-audit" {
		t.Errorf("index=%q, want mcp-audit", events[0].Index)
	}
	if events[0].Event["toolName"] != "test_tool" {
		t.Errorf("toolName=%v", events[0].Event["toolName"])
	}
}

func TestSplunkHECExporter_Shutdown(t *testing.T) {
	var mu sync.Mutex
	var callCount int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callCount++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{
		SplunkHECEndpoint:     srv.URL,
		SplunkHECToken:        "tok",
		SplunkHECBatchSize:    100, // won't trigger batch by size
		SplunkHECBatchTimeout: 60000,
	}

	e := NewSplunkHECExporter(cfg)

	e.Export(&receipt.Record{
		Timestamp:    time.Now().UnixMilli(),
		ToolName:     "test",
		MutationType: "readonly",
		Outcome:      "success",
	})

	// Shutdown should flush the remaining record
	e.Shutdown()

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	count := callCount
	mu.Unlock()

	if count != 1 {
		t.Errorf("expected 1 flush on shutdown, got %d", count)
	}
}

// jsonReader wraps a byte slice for json.NewDecoder
type jsonReaderType struct {
	data []byte
	pos  int
}

func jsonReader(data []byte) *jsonReaderType {
	return &jsonReaderType{data: data}
}

func (r *jsonReaderType) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}
