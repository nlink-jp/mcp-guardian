package config

import "fmt"

// Config holds the proxy configuration.
type Config struct {
	Transport    string // upstream transport: "stdio" (default) or "sse"
	Upstream     string   // upstream command binary (stdio transport)
	UpstreamArgs []string // upstream command arguments (stdio transport)
	UpstreamURL  string            // upstream MCP server URL (sse transport)
	SSEHeaders       map[string]string // additional HTTP headers for SSE transport
	OAuth2Flow       string            // "client_credentials" (default) or "authorization_code"
	OAuth2TokenURL   string            // OAuth2 token endpoint URL
	OAuth2ClientID   string            // OAuth2 client ID
	OAuth2ClientSecret string          // OAuth2 client secret
	OAuth2Scopes     []string          // OAuth2 scopes (space-separated in token request)
	TokenCommand     string            // external command to obtain a Bearer token
	TokenCommandArgs []string          // arguments for the token command
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
	SplunkHECEndpoint  string          // Splunk HEC endpoint URL (empty = disabled)
	SplunkHECToken     string          // Splunk HEC authentication token
	SplunkHECIndex     string          // Splunk index (empty = default)
	SplunkHECBatchSize int             // batch size (default: 10)
	SplunkHECBatchTimeout int          // batch timeout in ms (default: 5000)
	MaxReceiptAgeDays int              // auto-purge receipts older than N days (0 = no purge)
}

// Defaults returns a Config with default values.
func Defaults() *Config {
	return &Config{
		StateDir:          ".governance",
		Enforcement:       "strict",
		SchemaMode:        "warn",
		MaxCalls:          0,
		TimeoutMs:         300000,
		MaxReceiptAgeDays: 7,
	}
}

// Validate checks that configuration values are within allowed ranges.
func (c *Config) Validate() error {
	switch c.Transport {
	case "stdio", "":
		if c.Upstream == "" {
			return fmt.Errorf("upstream command is required for stdio transport")
		}
	case "sse":
		if c.UpstreamURL == "" {
			return fmt.Errorf("upstream URL is required for sse transport (use --upstream-url)")
		}
		if c.OAuth2TokenURL != "" && c.OAuth2Flow != "authorization_code" {
			if c.OAuth2ClientID == "" || c.OAuth2ClientSecret == "" {
				return fmt.Errorf("--oauth2-client-id and --oauth2-client-secret are required with --oauth2-token-url")
			}
		}
		if c.OAuth2TokenURL != "" && c.TokenCommand != "" {
			return fmt.Errorf("oauth2 and token-command are mutually exclusive")
		}
	default:
		return fmt.Errorf("transport must be 'stdio' or 'sse', got '%s'", c.Transport)
	}
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
	return nil
}
