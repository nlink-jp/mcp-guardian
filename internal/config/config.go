package config

// Config holds the proxy configuration.
type Config struct {
	Upstream     string
	UpstreamArgs []string
	StateDir     string
	Enforcement  string
	SchemaMode   string
	MaxCalls     int
	TimeoutMs    int
	WebhookURLs  []string
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
