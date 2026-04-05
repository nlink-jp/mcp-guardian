package transport

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// sseClientTransport implements Transport for MCP servers that speak
// the Streamable HTTP transport (formerly SSE transport).
//
// Protocol:
//   - Client sends JSON-RPC requests via HTTP POST to the server endpoint.
//   - Server responds with either:
//     (a) Content-Type: application/json — a single JSON-RPC response, or
//     (b) Content-Type: text/event-stream — an SSE stream carrying one or
//     more JSON-RPC messages as "message" events.
//   - The endpoint URL may be updated via the server's initial SSE response.
type sseClientTransport struct {
	endpoint string
	headers  map[string]string
	client   *http.Client
	auth     TokenProvider // nil if no auth configured

	// incoming receives messages parsed from HTTP responses.
	incoming chan []byte
	// closed is used to signal that Close has been called.
	closed chan struct{}

	mu       sync.Mutex
	isClosed bool

	// sessionID tracks the Mcp-Session-Id header for session affinity.
	sessionID string
}

// NewSSEClientTransport creates a new SSE client transport that connects
// to the given MCP server URL. Headers are sent with every HTTP request.
// If auth is non-nil, it is used to obtain Bearer tokens for authentication.
// When the server responds with 401, the token is invalidated and the
// request is retried once with a fresh token.
func NewSSEClientTransport(endpoint string, headers map[string]string, opts ...SSEOption) (Transport, error) {
	if endpoint == "" {
		return nil, fmt.Errorf("SSE endpoint URL is required")
	}

	t := &sseClientTransport{
		endpoint: endpoint,
		headers:  headers,
		client:   &http.Client{},
		incoming: make(chan []byte, 64),
		closed:   make(chan struct{}),
	}

	for _, opt := range opts {
		opt(t)
	}

	return t, nil
}

// SSEOption configures optional behavior for the SSE transport.
type SSEOption func(*sseClientTransport)

// WithTokenProvider sets a TokenProvider for OAuth2/Bearer authentication.
func WithTokenProvider(auth TokenProvider) SSEOption {
	return func(t *sseClientTransport) {
		t.auth = auth
	}
}

// Send posts a JSON-RPC message to the server endpoint via HTTP POST.
// The response is parsed and queued into the incoming channel.
// If a TokenProvider is configured and the server responds with 401,
// the token is invalidated and the request is retried once.
func (t *sseClientTransport) Send(data []byte) error {
	resp, err := t.doPost(data)
	if err != nil {
		return err
	}

	// Handle 401: invalidate token and retry once
	if resp.StatusCode == http.StatusUnauthorized && t.auth != nil {
		resp.Body.Close()
		t.auth.Invalidate()
		resp, err = t.doPost(data)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusUnauthorized {
			resp.Body.Close()
			return fmt.Errorf("HTTP 401: authentication failed after token refresh")
		}
	}

	return t.handleResponse(resp)
}

// doPost performs a single HTTP POST request with authentication headers.
func (t *sseClientTransport) doPost(data []byte) (*http.Response, error) {
	t.mu.Lock()
	if t.isClosed {
		t.mu.Unlock()
		return nil, fmt.Errorf("transport is closed")
	}
	endpoint := t.endpoint
	sessionID := t.sessionID
	t.mu.Unlock()

	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}

	// Apply TokenProvider (takes priority over static Authorization header)
	if t.auth != nil {
		token, err := t.auth.Token()
		if err != nil {
			return nil, fmt.Errorf("obtain auth token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}

	for k, v := range t.headers {
		req.Header.Set(k, v)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP POST: %w", err)
	}

	return resp, nil
}

// handleResponse processes a successful HTTP response.
func (t *sseClientTransport) handleResponse(resp *http.Response) error {
	// Track session ID from server response
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.mu.Lock()
		t.sessionID = sid
		t.mu.Unlock()
	}

	contentType := resp.Header.Get("Content-Type")

	switch {
	case strings.HasPrefix(contentType, "application/json"):
		// Single JSON-RPC response
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("read response body: %w", err)
		}
		select {
		case t.incoming <- body:
		case <-t.closed:
			return fmt.Errorf("transport is closed")
		}

	case strings.HasPrefix(contentType, "text/event-stream"):
		// SSE stream — parse in background goroutine
		go t.consumeSSEStream(resp.Body)

	default:
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncateBytes(body, 200))
		}
		// Unknown content type with success status — try to parse as JSON
		if len(body) > 0 {
			select {
			case t.incoming <- body:
			case <-t.closed:
				return fmt.Errorf("transport is closed")
			}
		}
	}

	return nil
}

// ReadLine reads the next JSON-RPC message from the incoming channel.
func (t *sseClientTransport) ReadLine() ([]byte, bool) {
	select {
	case data, ok := <-t.incoming:
		if !ok {
			return nil, false
		}
		return data, true
	case <-t.closed:
		return nil, false
	}
}

// Close shuts down the transport.
func (t *sseClientTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.isClosed {
		return nil
	}
	t.isClosed = true
	close(t.closed)

	// Send DELETE to terminate session if we have a session ID
	if t.sessionID != "" {
		req, err := http.NewRequest("DELETE", t.endpoint, nil)
		if err == nil {
			req.Header.Set("Mcp-Session-Id", t.sessionID)
			if t.auth != nil {
				if token, err := t.auth.Token(); err == nil {
					req.Header.Set("Authorization", "Bearer "+token)
				}
			}
			for k, v := range t.headers {
				req.Header.Set(k, v)
			}
			resp, err := t.client.Do(req)
			if err == nil {
				resp.Body.Close()
			}
		}
	}

	return nil
}

// consumeSSEStream reads an SSE stream and dispatches JSON-RPC messages
// to the incoming channel.
func (t *sseClientTransport) consumeSSEStream(body io.ReadCloser) {
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer

	var eventType string
	var dataLines []string

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			// Empty line = end of event
			if len(dataLines) > 0 {
				data := strings.Join(dataLines, "\n")
				if eventType == "" || eventType == "message" {
					t.dispatchSSEData(data)
				}
			}
			eventType = ""
			dataLines = nil
			continue
		}

		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data:"))
		}
		// Ignore id:, retry:, and comment lines (starting with :)
	}

	// Flush any remaining event data
	if len(dataLines) > 0 {
		data := strings.Join(dataLines, "\n")
		if eventType == "" || eventType == "message" {
			t.dispatchSSEData(data)
		}
	}
}

// dispatchSSEData parses SSE data as JSON-RPC and sends to incoming channel.
// The data may be a single JSON-RPC message or a JSON array of messages.
func (t *sseClientTransport) dispatchSSEData(data string) {
	trimmed := strings.TrimSpace(data)
	if len(trimmed) == 0 {
		return
	}

	// Check if it's a JSON array (batch response)
	if trimmed[0] == '[' {
		var batch []json.RawMessage
		if json.Unmarshal([]byte(trimmed), &batch) == nil {
			for _, msg := range batch {
				select {
				case t.incoming <- []byte(msg):
				case <-t.closed:
					return
				}
			}
			return
		}
	}

	// Single message
	select {
	case t.incoming <- []byte(trimmed):
	case <-t.closed:
	}
}

func truncateBytes(b []byte, maxLen int) string {
	if len(b) <= maxLen {
		return string(b)
	}
	return string(b[:maxLen]) + "..."
}
