package otlp

// OTLP/HTTP JSON type definitions.
// These structs mirror the OpenTelemetry Protocol JSON encoding
// for Logs and Traces signals.
// Reference: https://opentelemetry.io/docs/specs/otlp/

// --- Common types ---

// Resource describes the entity producing telemetry.
type Resource struct {
	Attributes []KeyValue `json:"attributes"`
}

// InstrumentationScope identifies the instrumentation library.
type InstrumentationScope struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// KeyValue is an OTLP attribute key-value pair.
type KeyValue struct {
	Key   string     `json:"key"`
	Value AnyValue   `json:"value"`
}

// AnyValue holds a typed attribute value.
// Only one field should be set.
type AnyValue struct {
	StringValue *string `json:"stringValue,omitempty"`
	IntValue    *string `json:"intValue,omitempty"` // JSON number as string per OTLP spec
	BoolValue   *bool   `json:"boolValue,omitempty"`
}

// StringVal creates a string AnyValue.
func StringVal(s string) AnyValue {
	return AnyValue{StringValue: &s}
}

// IntVal creates an integer AnyValue (OTLP encodes int64 as string).
func IntVal(n int64) AnyValue {
	s := intToString(n)
	return AnyValue{IntValue: &s}
}

// BoolVal creates a boolean AnyValue.
func BoolVal(b bool) AnyValue {
	return AnyValue{BoolValue: &b}
}

// --- Logs types ---

// LogsPayload is the top-level OTLP Logs export request.
type LogsPayload struct {
	ResourceLogs []ResourceLogs `json:"resourceLogs"`
}

// ResourceLogs groups log records by resource.
type ResourceLogs struct {
	Resource  Resource    `json:"resource"`
	ScopeLogs []ScopeLogs `json:"scopeLogs"`
}

// ScopeLogs groups log records by instrumentation scope.
type ScopeLogs struct {
	Scope      InstrumentationScope `json:"scope"`
	LogRecords []LogRecord          `json:"logRecords"`
}

// LogRecord represents a single OTLP log record.
type LogRecord struct {
	TimeUnixNano   string     `json:"timeUnixNano"`
	SeverityNumber int        `json:"severityNumber"`
	SeverityText   string     `json:"severityText"`
	Body           AnyValue   `json:"body"`
	Attributes     []KeyValue `json:"attributes"`
}

// --- Traces types ---

// TracesPayload is the top-level OTLP Traces export request.
type TracesPayload struct {
	ResourceSpans []ResourceSpans `json:"resourceSpans"`
}

// ResourceSpans groups spans by resource.
type ResourceSpans struct {
	Resource   Resource     `json:"resource"`
	ScopeSpans []ScopeSpans `json:"scopeSpans"`
}

// ScopeSpans groups spans by instrumentation scope.
type ScopeSpans struct {
	Scope InstrumentationScope `json:"scope"`
	Spans []Span               `json:"spans"`
}

// Span represents a single OTLP trace span.
type Span struct {
	TraceID            string     `json:"traceId"`
	SpanID             string     `json:"spanId"`
	Name               string     `json:"name"`
	Kind               int        `json:"kind"`
	StartTimeUnixNano  string     `json:"startTimeUnixNano"`
	EndTimeUnixNano    string     `json:"endTimeUnixNano"`
	Status             SpanStatus `json:"status"`
	Attributes         []KeyValue `json:"attributes"`
}

// SpanStatus represents the status of a span.
type SpanStatus struct {
	Code    int    `json:"code"`
	Message string `json:"message,omitempty"`
}

// Span kind constants.
const (
	SpanKindClient = 3
)

// Span status code constants.
const (
	StatusCodeUnset = 0
	StatusCodeOK    = 1
	StatusCodeError = 2
)

// Severity constants.
const (
	SeverityINFO = 9
	SeverityWARN = 13
)

func intToString(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
