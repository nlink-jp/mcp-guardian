// Package export defines the Exporter interface for telemetry backends.
// Implementations include OTLP and Splunk HEC.
package export

import "github.com/nlink-jp/mcp-guardian/internal/receipt"

// Exporter sends receipt records to a telemetry backend.
// Implementations handle batching, serialization, and delivery internally.
type Exporter interface {
	// Export adds a receipt record to the export buffer.
	Export(r *receipt.Record)

	// Shutdown flushes remaining records and releases resources.
	Shutdown()
}
