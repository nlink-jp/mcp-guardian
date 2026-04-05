package config

import "fmt"

// Config holds the proxy configuration.
type Config struct {
	Upstream     string   // upstream command binary
	UpstreamArgs []string // upstream command arguments
	StateDir     string
	Enforcement  string // "strict" or "advisory"
	SchemaMode   string // "off", "warn", or "strict"
	MaxCalls     int    // 0 = unlimited
	TimeoutMs    int
	WebhookURLs  []string
	MaskPatterns     []string         // tool name glob patterns to mask
	OTLPEndpoint     string            // OTLP/HTTP base URL (empty = disabled)
	OTLPHeaders      map[string]string // additional HTTP headers for OTLP
	OTLPBatchSize    int               // batch size (default: 10)
	OTLPBatchTimeout int               // batch timeout in ms (default: 5000)
}

// Defaults returns a Config with default values.
func Defaults() *Config {
	return &Config{
		StateDir:    ".governance",
		Enforcement: "strict",
		SchemaMode:  "warn",
		MaxCalls:    0,
		TimeoutMs:   300000,
	}
}

// Validate checks that configuration values are within allowed ranges.
func (c *Config) Validate() error {
	switch c.Enforcement {
	case "strict", "advisory":
	default:
		return fmt.Errorf("enforcement must be 'strict' or 'advisory', got '%s'", c.Enforcement)
	}
	switch c.SchemaMode {
	case "off", "warn", "strict":
	default:
		return fmt.Errorf("schema must be 'off', 'warn', or 'strict', got '%s'", c.SchemaMode)
	}
	if c.MaxCalls < 0 {
		return fmt.Errorf("max-calls must be >= 0, got %d", c.MaxCalls)
	}
	if c.TimeoutMs <= 0 {
		return fmt.Errorf("timeout must be > 0, got %d", c.TimeoutMs)
	}
	if c.Upstream == "" {
		return fmt.Errorf("upstream command is required")
	}
	return nil
}
