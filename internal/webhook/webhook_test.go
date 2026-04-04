package webhook

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestFireSendsPayload(t *testing.T) {
	var mu sync.Mutex
	var received []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		received = buf[:n]
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	Fire([]string{srv.URL}, EventBlocked, map[string]interface{}{
		"toolName": "write_file",
		"reason":   "budget exceeded",
	})

	// Wait for goroutine
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(received) == 0 {
		t.Fatal("webhook did not receive payload")
	}

	var payload Payload
	if err := json.Unmarshal(received, &payload); err != nil {
		t.Fatalf("invalid payload JSON: %v", err)
	}
	if payload.Event != EventBlocked {
		t.Errorf("expected event=%s, got %s", EventBlocked, payload.Event)
	}
	if payload.Timestamp == 0 {
		t.Error("timestamp should not be zero")
	}
}

func TestFireNoURLs(t *testing.T) {
	// Should not panic
	Fire(nil, EventBlocked, nil)
	Fire([]string{}, EventSessionComplete, nil)
}

func TestFireUnreachable(t *testing.T) {
	// Should not hang or panic
	Fire([]string{"http://192.0.2.1:1/"}, EventBlocked, nil)
	time.Sleep(100 * time.Millisecond)
}

func TestFormatForTargetDiscord(t *testing.T) {
	payload := Payload{
		Event:     EventBlocked,
		Timestamp: time.Now().UnixMilli(),
		Data: map[string]interface{}{
			"toolName": "write_file",
			"reason":   "budget exceeded",
		},
	}
	result := formatForTarget("https://discord.com/api/webhooks/123/abc", payload)
	m, ok := result.(map[string]string)
	if !ok {
		t.Fatal("expected map[string]string for discord")
	}
	if m["content"] == "" {
		t.Error("discord content should not be empty")
	}
}

func TestFormatForTargetTelegram(t *testing.T) {
	payload := Payload{
		Event:     EventLoopDetected,
		Timestamp: time.Now().UnixMilli(),
		Data: map[string]interface{}{
			"toolName": "read_file",
		},
	}
	result := formatForTarget("https://api.telegram.org/bot123/sendMessage", payload)
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("expected map[string]interface{} for telegram")
	}
	if m["parse_mode"] != "Markdown" {
		t.Error("telegram should use Markdown parse_mode")
	}
}

func TestFormatForTargetGeneric(t *testing.T) {
	payload := Payload{Event: EventSessionComplete}
	result := formatForTarget("https://example.com/webhook", payload)
	p, ok := result.(Payload)
	if !ok {
		t.Fatal("expected Payload for generic webhook")
	}
	if p.Event != EventSessionComplete {
		t.Error("event should be preserved")
	}
}
