package jsonrpc

import (
	"encoding/json"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid request", `{"jsonrpc":"2.0","id":"1","method":"tools/call","params":{}}`, false},
		{"valid notification", `{"jsonrpc":"2.0","method":"notifications/cancelled"}`, false},
		{"valid response", `{"jsonrpc":"2.0","id":"1","result":{}}`, false},
		{"invalid json", `{broken`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := Parse([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Errorf("Parse() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && msg.JSONRPC != "2.0" {
				t.Errorf("expected jsonrpc=2.0, got %s", msg.JSONRPC)
			}
		})
	}
}

func TestMessageClassification(t *testing.T) {
	tests := []struct {
		name           string
		msg            Message
		isRequest      bool
		isNotification bool
		isResponse     bool
	}{
		{
			"request",
			Message{Method: "tools/call", ID: json.RawMessage(`"1"`)},
			true, false, false,
		},
		{
			"notification",
			Message{Method: "notifications/cancelled"},
			false, true, false,
		},
		{
			"notification with null id",
			Message{Method: "notifications/cancelled", ID: json.RawMessage(`null`)},
			false, true, false,
		},
		{
			"success response",
			Message{ID: json.RawMessage(`"1"`), Result: json.RawMessage(`{}`)},
			false, false, true,
		},
		{
			"error response",
			Message{ID: json.RawMessage(`"1"`), Error: &RPCError{Code: -1, Message: "fail"}},
			false, false, true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.msg.IsRequest(); got != tt.isRequest {
				t.Errorf("IsRequest() = %v, want %v", got, tt.isRequest)
			}
			if got := tt.msg.IsNotification(); got != tt.isNotification {
				t.Errorf("IsNotification() = %v, want %v", got, tt.isNotification)
			}
			if got := tt.msg.IsResponse(); got != tt.isResponse {
				t.Errorf("IsResponse() = %v, want %v", got, tt.isResponse)
			}
		})
	}
}

func TestIDString(t *testing.T) {
	tests := []struct {
		name string
		id   json.RawMessage
		want string
	}{
		{"string id", json.RawMessage(`"abc"`), `"abc"`},
		{"numeric id", json.RawMessage(`42`), `42`},
		{"null id", json.RawMessage(`null`), ""},
		{"empty id", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := Message{ID: tt.id}
			if got := msg.IDString(); got != tt.want {
				t.Errorf("IDString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewErrorResponse(t *testing.T) {
	id := json.RawMessage(`"1"`)
	msg := NewErrorResponse(id, -32600, "invalid request")
	if msg.JSONRPC != "2.0" {
		t.Errorf("expected jsonrpc=2.0")
	}
	if msg.Error == nil || msg.Error.Code != -32600 {
		t.Errorf("expected error code -32600")
	}
}

func TestNewResultResponse(t *testing.T) {
	id := json.RawMessage(`"1"`)
	msg, err := NewResultResponse(id, map[string]string{"key": "val"})
	if err != nil {
		t.Fatal(err)
	}
	if msg.Result == nil {
		t.Error("expected non-nil result")
	}
}

func TestParseToolCallParams(t *testing.T) {
	raw := json.RawMessage(`{"name":"write_file","arguments":{"path":"/tmp/x","content":"hello"}}`)
	p, err := ParseToolCallParams(raw)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "write_file" {
		t.Errorf("expected name=write_file, got %s", p.Name)
	}
	if p.Arguments["path"] != "/tmp/x" {
		t.Errorf("expected path=/tmp/x, got %v", p.Arguments["path"])
	}
}

func TestParseToolResult(t *testing.T) {
	raw := json.RawMessage(`{"content":[{"type":"text","text":"ok"}],"isError":false}`)
	r, err := ParseToolResult(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Content) != 1 || r.Content[0].Text != "ok" {
		t.Error("unexpected tool result content")
	}
	if r.IsError {
		t.Error("expected isError=false")
	}
}
