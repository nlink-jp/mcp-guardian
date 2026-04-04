package jsonrpc

import "encoding/json"

// Message represents a raw JSON-RPC 2.0 message (request, response, or notification).
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError represents a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// IsRequest returns true if this message is a request (has method and id).
func (m *Message) IsRequest() bool {
	return m.Method != "" && len(m.ID) > 0 && string(m.ID) != "null"
}

// IsNotification returns true if this message is a notification (has method but no id).
func (m *Message) IsNotification() bool {
	return m.Method != "" && (len(m.ID) == 0 || string(m.ID) == "null")
}

// IsResponse returns true if this message is a response (has result or error, no method).
func (m *Message) IsResponse() bool {
	return m.Method == "" && (m.Result != nil || m.Error != nil)
}

// IDString returns the ID as a comparable string. Returns "" for notifications.
func (m *Message) IDString() string {
	if len(m.ID) == 0 || string(m.ID) == "null" {
		return ""
	}
	return string(m.ID)
}

// Parse parses a raw JSON line into a Message.
func Parse(data []byte) (*Message, error) {
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// NewErrorResponse creates a JSON-RPC error response.
func NewErrorResponse(id json.RawMessage, code int, message string) *Message {
	return &Message{
		JSONRPC: "2.0",
		ID:      id,
		Error: &RPCError{
			Code:    code,
			Message: message,
		},
	}
}

// NewResultResponse creates a JSON-RPC success response.
func NewResultResponse(id json.RawMessage, result interface{}) (*Message, error) {
	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return &Message{
		JSONRPC: "2.0",
		ID:      id,
		Result:  json.RawMessage(data),
	}, nil
}

// Marshal serializes a message to JSON bytes.
func Marshal(msg *Message) ([]byte, error) {
	return json.Marshal(msg)
}

// ToolCallParams represents the params for a tools/call request.
type ToolCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

// ParseToolCallParams extracts tool call parameters from a message.
func ParseToolCallParams(params json.RawMessage) (*ToolCallParams, error) {
	var p ToolCallParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// ToolResult represents the content of a tool call response.
type ToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ToolContent represents a single content item in a tool result.
type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// ParseToolResult extracts the tool result from a response message.
func ParseToolResult(result json.RawMessage) (*ToolResult, error) {
	var r ToolResult
	if err := json.Unmarshal(result, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ToolInfo represents a tool definition in a tools/list response.
type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// ToolsListResult represents the result of a tools/list response.
type ToolsListResult struct {
	Tools []ToolInfo `json:"tools"`
}
