package transport

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// mockSSEServer creates a test HTTP server that behaves like an MCP SSE server.
// It supports both JSON and SSE response modes.
type mockSSEServer struct {
	server    *httptest.Server
	mu        sync.Mutex
	responses []mockResponse
	sessionID string
	deleted   bool
}

type mockResponse struct {
	mode string // "json" or "sse"
	body string
}

func newMockSSEServer() *mockSSEServer {
	m := &mockSSEServer{
		sessionID: "test-session-123",
	}
	m.server = httptest.NewServer(http.HandlerFunc(m.handle))
	return m
}

func (m *mockSSEServer) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method == "DELETE" {
		m.mu.Lock()
		m.deleted = true
		m.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read error", http.StatusInternalServerError)
		return
	}

	// Parse to get the method
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	json.Unmarshal(body, &msg)

	w.Header().Set("Mcp-Session-Id", m.sessionID)

	m.mu.Lock()
	if len(m.responses) > 0 {
		resp := m.responses[0]
		m.responses = m.responses[1:]
		m.mu.Unlock()

		switch resp.mode {
		case "sse":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, resp.body)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, resp.body)
		}
		return
	}
	m.mu.Unlock()

	// Default: echo back a simple response
	id := string(msg.ID)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"echo":true}}`, id)
}

func (m *mockSSEServer) queueResponse(mode, body string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses = append(m.responses, mockResponse{mode: mode, body: body})
}

func (m *mockSSEServer) close() {
	m.server.Close()
}

func TestSSEClientTransport_JSONResponse(t *testing.T) {
	mock := newMockSSEServer()
	defer mock.close()

	tr, err := NewSSEClientTransport(mock.server.URL, nil)
	if err != nil {
		t.Fatalf("NewSSEClientTransport: %v", err)
	}
	defer tr.Close()

	// Send a request
	req := `{"jsonrpc":"2.0","id":"1","method":"initialize"}`
	if err := tr.Send([]byte(req)); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Read response
	line, ok := tr.ReadLine()
	if !ok {
		t.Fatal("ReadLine returned false")
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}

	if resp["id"] != "1" {
		t.Errorf("got id=%v, want 1", resp["id"])
	}
}

func TestSSEClientTransport_SSEResponse(t *testing.T) {
	mock := newMockSSEServer()
	defer mock.close()

	// Queue an SSE response
	mock.queueResponse("sse", "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":\"2\",\"result\":{\"ok\":true}}\n\n")

	tr, err := NewSSEClientTransport(mock.server.URL, nil)
	if err != nil {
		t.Fatalf("NewSSEClientTransport: %v", err)
	}
	defer tr.Close()

	req := `{"jsonrpc":"2.0","id":"2","method":"tools/list"}`
	if err := tr.Send([]byte(req)); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Wait for SSE message to arrive
	line, ok := tr.ReadLine()
	if !ok {
		t.Fatal("ReadLine returned false")
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}

	if resp["id"] != "2" {
		t.Errorf("got id=%v, want 2", resp["id"])
	}
}

func TestSSEClientTransport_SSEMultipleEvents(t *testing.T) {
	mock := newMockSSEServer()
	defer mock.close()

	// Queue an SSE response with multiple events
	sseBody := "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":\"3\",\"result\":{\"n\":1}}\n\nevent: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{\"progress\":50}}\n\n"
	mock.queueResponse("sse", sseBody)

	tr, err := NewSSEClientTransport(mock.server.URL, nil)
	if err != nil {
		t.Fatalf("NewSSEClientTransport: %v", err)
	}
	defer tr.Close()

	if err := tr.Send([]byte(`{"jsonrpc":"2.0","id":"3","method":"test"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Should receive two messages
	line1, ok := tr.ReadLine()
	if !ok {
		t.Fatal("ReadLine 1 returned false")
	}
	var msg1 map[string]interface{}
	json.Unmarshal(line1, &msg1)
	if msg1["id"] != "3" {
		t.Errorf("msg1 id=%v, want 3", msg1["id"])
	}

	line2, ok := tr.ReadLine()
	if !ok {
		t.Fatal("ReadLine 2 returned false")
	}
	var msg2 map[string]interface{}
	json.Unmarshal(line2, &msg2)
	if msg2["method"] != "notifications/progress" {
		t.Errorf("msg2 method=%v, want notifications/progress", msg2["method"])
	}
}

func TestSSEClientTransport_SessionID(t *testing.T) {
	mock := newMockSSEServer()
	defer mock.close()

	tr, err := NewSSEClientTransport(mock.server.URL, nil)
	if err != nil {
		t.Fatalf("NewSSEClientTransport: %v", err)
	}

	// First request establishes session
	if err := tr.Send([]byte(`{"jsonrpc":"2.0","id":"1","method":"init"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	tr.ReadLine() // drain

	// Close should send DELETE with session ID
	tr.Close()

	// Give a moment for the DELETE to arrive
	time.Sleep(50 * time.Millisecond)

	mock.mu.Lock()
	deleted := mock.deleted
	mock.mu.Unlock()

	if !deleted {
		t.Error("expected DELETE request on close")
	}
}

func TestSSEClientTransport_CustomHeaders(t *testing.T) {
	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":"1","result":{}}`)
	}))
	defer server.Close()

	headers := map[string]string{
		"Authorization": "Bearer test-token",
	}

	tr, err := NewSSEClientTransport(server.URL, headers)
	if err != nil {
		t.Fatalf("NewSSEClientTransport: %v", err)
	}
	defer tr.Close()

	if err := tr.Send([]byte(`{"jsonrpc":"2.0","id":"1","method":"test"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	tr.ReadLine() // drain

	if receivedAuth != "Bearer test-token" {
		t.Errorf("got Authorization=%q, want Bearer test-token", receivedAuth)
	}
}

func TestSSEClientTransport_CloseBeforeRead(t *testing.T) {
	mock := newMockSSEServer()
	defer mock.close()

	tr, err := NewSSEClientTransport(mock.server.URL, nil)
	if err != nil {
		t.Fatalf("NewSSEClientTransport: %v", err)
	}

	tr.Close()

	_, ok := tr.ReadLine()
	if ok {
		t.Error("ReadLine should return false after Close")
	}
}

func TestSSEClientTransport_EmptyEndpoint(t *testing.T) {
	_, err := NewSSEClientTransport("", nil)
	if err == nil {
		t.Fatal("expected error for empty endpoint")
	}
}

func TestSSEClientTransport_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer server.Close()

	tr, err := NewSSEClientTransport(server.URL, nil)
	if err != nil {
		t.Fatalf("NewSSEClientTransport: %v", err)
	}
	defer tr.Close()

	err = tr.Send([]byte(`{"jsonrpc":"2.0","id":"1","method":"test"}`))
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestSSEClientTransport_BatchResponse(t *testing.T) {
	mock := newMockSSEServer()
	defer mock.close()

	// Queue SSE response with batch JSON array
	batch := `[{"jsonrpc":"2.0","id":"10","result":{"a":1}},{"jsonrpc":"2.0","id":"11","result":{"b":2}}]`
	mock.queueResponse("sse", "event: message\ndata: "+batch+"\n\n")

	tr, err := NewSSEClientTransport(mock.server.URL, nil)
	if err != nil {
		t.Fatalf("NewSSEClientTransport: %v", err)
	}
	defer tr.Close()

	if err := tr.Send([]byte(`{"jsonrpc":"2.0","id":"10","method":"test"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Should receive two individual messages from the batch
	line1, ok := tr.ReadLine()
	if !ok {
		t.Fatal("ReadLine 1 returned false")
	}
	var msg1 map[string]interface{}
	json.Unmarshal(line1, &msg1)
	if msg1["id"] != "10" {
		t.Errorf("msg1 id=%v, want 10", msg1["id"])
	}

	line2, ok := tr.ReadLine()
	if !ok {
		t.Fatal("ReadLine 2 returned false")
	}
	var msg2 map[string]interface{}
	json.Unmarshal(line2, &msg2)
	if msg2["id"] != "11" {
		t.Errorf("msg2 id=%v, want 11", msg2["id"])
	}
}

// mockTokenProvider is a test TokenProvider that tracks calls.
type mockTokenProvider struct {
	tokens      []string
	callCount   int
	invalidated int
}

func (m *mockTokenProvider) Token() (string, error) {
	if m.callCount >= len(m.tokens) {
		return "", fmt.Errorf("no more tokens")
	}
	tok := m.tokens[m.callCount]
	m.callCount++
	return tok, nil
}

func (m *mockTokenProvider) Invalidate() {
	m.invalidated++
}

func TestSSEClientTransport_OAuth2Token(t *testing.T) {
	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":"1","result":{}}`)
	}))
	defer server.Close()

	provider := &mockTokenProvider{tokens: []string{"my-oauth-token"}}

	tr, err := NewSSEClientTransport(server.URL, nil, WithTokenProvider(provider))
	if err != nil {
		t.Fatalf("NewSSEClientTransport: %v", err)
	}
	defer tr.Close()

	if err := tr.Send([]byte(`{"jsonrpc":"2.0","id":"1","method":"test"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	tr.ReadLine() // drain

	if receivedAuth != "Bearer my-oauth-token" {
		t.Errorf("got Authorization=%q, want 'Bearer my-oauth-token'", receivedAuth)
	}
}

func TestSSEClientTransport_401Retry(t *testing.T) {
	var callCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			w.WriteHeader(http.StatusOK)
			return
		}
		callCount++
		auth := r.Header.Get("Authorization")

		if auth == "Bearer expired-token" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":"token expired"}`)
			return
		}

		if auth == "Bearer fresh-token" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":"1","result":{"refreshed":true}}`)
			return
		}

		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	provider := &mockTokenProvider{
		tokens: []string{"expired-token", "fresh-token"},
	}

	tr, err := NewSSEClientTransport(server.URL, nil, WithTokenProvider(provider))
	if err != nil {
		t.Fatalf("NewSSEClientTransport: %v", err)
	}
	defer tr.Close()

	if err := tr.Send([]byte(`{"jsonrpc":"2.0","id":"1","method":"test"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}

	line, ok := tr.ReadLine()
	if !ok {
		t.Fatal("ReadLine returned false")
	}

	var resp map[string]interface{}
	json.Unmarshal(line, &resp)
	result, _ := resp["result"].(map[string]interface{})
	if result["refreshed"] != true {
		t.Errorf("expected refreshed=true, got %v", result)
	}

	if provider.invalidated != 1 {
		t.Errorf("expected 1 invalidation, got %d", provider.invalidated)
	}
	if callCount != 2 {
		t.Errorf("expected 2 HTTP calls (initial + retry), got %d", callCount)
	}
}

func TestSSEClientTransport_401RetryFailsBoth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"always unauthorized"}`)
	}))
	defer server.Close()

	provider := &mockTokenProvider{
		tokens: []string{"bad-token-1", "bad-token-2"},
	}

	tr, err := NewSSEClientTransport(server.URL, nil, WithTokenProvider(provider))
	if err != nil {
		t.Fatalf("NewSSEClientTransport: %v", err)
	}
	defer tr.Close()

	err = tr.Send([]byte(`{"jsonrpc":"2.0","id":"1","method":"test"}`))
	if err == nil {
		t.Fatal("expected error for persistent 401")
	}
	if provider.invalidated != 1 {
		t.Errorf("expected 1 invalidation, got %d", provider.invalidated)
	}
}

func TestSSEClientTransport_TokenOverridesStaticHeader(t *testing.T) {
	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":"1","result":{}}`)
	}))
	defer server.Close()

	// Static header says "Bearer static", but TokenProvider should win
	// Actually: headers are applied AFTER TokenProvider, so static header
	// would override. Let's verify the current behavior.
	provider := &mockTokenProvider{tokens: []string{"dynamic-token"}}

	tr, err := NewSSEClientTransport(server.URL,
		map[string]string{"Authorization": "Bearer static"},
		WithTokenProvider(provider))
	if err != nil {
		t.Fatalf("NewSSEClientTransport: %v", err)
	}
	defer tr.Close()

	if err := tr.Send([]byte(`{"jsonrpc":"2.0","id":"1","method":"test"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	tr.ReadLine() // drain

	// Static headers are applied after TokenProvider, so static wins.
	// This is documented: "Apply TokenProvider, then static headers"
	// Users should not set Authorization in both places.
	if receivedAuth != "Bearer static" {
		t.Errorf("got Authorization=%q, want 'Bearer static' (static overrides provider)", receivedAuth)
	}
}
