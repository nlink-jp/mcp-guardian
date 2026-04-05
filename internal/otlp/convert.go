package otlp

import (
	"crypto/sha256"
	"fmt"

	"github.com/nlink-jp/mcp-guardian/internal/receipt"
)

// ConvertToLogRecord converts a receipt.Record to an OTLP LogRecord.
func ConvertToLogRecord(r *receipt.Record) LogRecord {
	sevNum := SeverityINFO
	sevText := "INFO"
	if r.Outcome == "blocked" || r.Outcome == "error" {
		sevNum = SeverityWARN
		sevText = "WARN"
	}

	attrs := buildAttributes(r)

	return LogRecord{
		TimeUnixNano:   msToNanoString(r.Timestamp),
		SeverityNumber: sevNum,
		SeverityText:   sevText,
		Body:           StringVal(r.Title),
		Attributes:     attrs,
	}
}

// ConvertToSpan converts a receipt.Record to an OTLP Span.
func ConvertToSpan(r *receipt.Record, traceID string) Span {
	startNano := msToNanoString(r.Timestamp)
	endNano := msToNanoString(r.Timestamp + r.DurationMs)

	statusCode := StatusCodeOK
	statusMsg := ""
	if r.Outcome == "error" || r.Outcome == "blocked" {
		statusCode = StatusCodeError
		statusMsg = r.Summary
	}

	spanID := deriveSpanID(r.Hash)
	attrs := buildAttributes(r)

	return Span{
		TraceID:           traceID,
		SpanID:            spanID,
		Name:              r.ToolName,
		Kind:              SpanKindClient,
		StartTimeUnixNano: startNano,
		EndTimeUnixNano:   endNano,
		Status: SpanStatus{
			Code:    statusCode,
			Message: statusMsg,
		},
		Attributes: attrs,
	}
}

// DeriveTraceID generates a deterministic trace ID from controller ID and epoch.
func DeriveTraceID(controllerID string, epoch int) string {
	data := fmt.Sprintf("%s:%d", controllerID, epoch)
	sum := sha256.Sum256([]byte(data))
	return fmt.Sprintf("%x", sum[:16])
}

func deriveSpanID(hash string) string {
	if len(hash) >= 16 {
		return hash[:16]
	}
	return fmt.Sprintf("%016s", hash)
}

func buildAttributes(r *receipt.Record) []KeyValue {
	attrs := []KeyValue{
		{Key: "mcp.tool.name", Value: StringVal(r.ToolName)},
		{Key: "mcp.tool.target", Value: StringVal(r.Target)},
		{Key: "mcp.outcome", Value: StringVal(r.Outcome)},
		{Key: "mcp.mutation_type", Value: StringVal(r.MutationType)},
		{Key: "mcp.duration_ms", Value: IntVal(r.DurationMs)},
		{Key: "mcp.receipt.seq", Value: IntVal(int64(r.Seq))},
		{Key: "mcp.receipt.hash", Value: StringVal(r.Hash)},
	}

	if r.ConvergenceSignal != "" {
		attrs = append(attrs, KeyValue{Key: "mcp.convergence_signal", Value: StringVal(r.ConvergenceSignal)})
	}
	if r.Summary != "" {
		attrs = append(attrs, KeyValue{Key: "mcp.summary", Value: StringVal(r.Summary)})
	}
	if r.ErrorText != "" {
		attrs = append(attrs, KeyValue{Key: "mcp.error_text", Value: StringVal(r.ErrorText)})
	}

	return attrs
}

func msToNanoString(ms int64) string {
	return intToString(ms * 1_000_000)
}
