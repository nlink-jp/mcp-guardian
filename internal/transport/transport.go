// Package transport defines the Transport interface for MCP message channels
// and provides implementations for stdio (process) and SSE (HTTP) transports.
package transport

import "io"

// Transport represents a bidirectional JSON-RPC message channel.
// Implementations handle the framing details of each transport type
// (newline-delimited stdio, SSE streams, etc.).
type Transport interface {
	// Send writes a JSON-RPC message to the remote endpoint.
	Send(data []byte) error

	// ReadLine reads the next JSON-RPC message from the remote endpoint.
	// Returns false when no more messages are available (connection closed).
	ReadLine() ([]byte, bool)

	// Close shuts down the transport and releases resources.
	Close() error
}

// ProcessTransportResult holds a ProcessTransport plus its stderr reader.
// Stderr is specific to process-based transports and not part of the
// Transport interface.
type ProcessTransportResult struct {
	Transport Transport
	Stderr    io.ReadCloser
}
