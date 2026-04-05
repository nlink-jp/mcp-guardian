package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// GlobalConfig represents the global/system configuration file.
// Contains telemetry settings and default values for all MCP server instances.
type GlobalConfig struct {
	OTLP     *OTLPConfig    `json:"otlp,omitempty"`
	Webhooks []string       `json:"webhooks,omitempty"`
	Defaults *DefaultsBlock `json:"defaults,omitempty"`
}

// DefaultsBlock holds default values for per-server settings.
type DefaultsBlock struct {
	Enforcement string `json:"enforcement,omitempty"`
	Schema      string `json:"schema,omitempty"`
	MaxCalls    *int   `json:"maxCalls,omitempty"`
	Timeout     *int   `json:"timeout,omitempty"`
}

// OTLPConfig holds OTLP export settings.
type OTLPConfig struct {
	Endpoint     string            `json:"endpoint,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	BatchSize    *int              `json:"batchSize,omitempty"`
	BatchTimeout *int              `json:"batchTimeout,omitempty"`
}

// ServerConfig represents the per-MCP-server configuration file.
type ServerConfig struct {
	Enforcement string   `json:"enforcement,omitempty"`
	Schema      string   `json:"schema,omitempty"`
	MaxCalls    *int     `json:"maxCalls,omitempty"`
	Timeout     *int     `json:"timeout,omitempty"`
	StateDir    string   `json:"stateDir,omitempty"`
	Mask        []string `json:"mask,omitempty"`
}

// LoadGlobal reads and parses a global configuration file.
func LoadGlobal(path string) (*GlobalConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read global config: %w", err)
	}
	var gc GlobalConfig
	if err := json.Unmarshal(data, &gc); err != nil {
		return nil, fmt.Errorf("parse global config: %w", err)
	}
	return &gc, nil
}

// LoadServer reads and parses a per-server configuration file.
func LoadServer(path string) (*ServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read server config: %w", err)
	}
	var sc ServerConfig
	if err := json.Unmarshal(data, &sc); err != nil {
		return nil, fmt.Errorf("parse server config: %w", err)
	}
	return &sc, nil
}

// ApplyTo merges global config into a Config.
// Applies defaults block as base values, plus OTLP and webhooks.
func (gc *GlobalConfig) ApplyTo(cfg *Config) {
	if gc.Defaults != nil {
		if gc.Defaults.Enforcement != "" {
			cfg.Enforcement = gc.Defaults.Enforcement
		}
		if gc.Defaults.Schema != "" {
			cfg.SchemaMode = gc.Defaults.Schema
		}
		if gc.Defaults.MaxCalls != nil {
			cfg.MaxCalls = *gc.Defaults.MaxCalls
		}
		if gc.Defaults.Timeout != nil {
			cfg.TimeoutMs = *gc.Defaults.Timeout
		}
	}
	if len(gc.Webhooks) > 0 {
		cfg.WebhookURLs = append(gc.Webhooks, cfg.WebhookURLs...)
	}
	if gc.OTLP != nil {
		if gc.OTLP.Endpoint != "" {
			cfg.OTLPEndpoint = gc.OTLP.Endpoint
		}
		if len(gc.OTLP.Headers) > 0 {
			if cfg.OTLPHeaders == nil {
				cfg.OTLPHeaders = make(map[string]string)
			}
			for k, v := range gc.OTLP.Headers {
				cfg.OTLPHeaders[k] = v
			}
		}
		if gc.OTLP.BatchSize != nil {
			cfg.OTLPBatchSize = *gc.OTLP.BatchSize
		}
		if gc.OTLP.BatchTimeout != nil {
			cfg.OTLPBatchTimeout = *gc.OTLP.BatchTimeout
		}
	}
}

// ApplyTo merges server config into a Config.
// Only non-zero/non-nil values are applied. Mask patterns are prepended.
func (sc *ServerConfig) ApplyTo(cfg *Config) {
	if sc.Enforcement != "" {
		cfg.Enforcement = sc.Enforcement
	}
	if sc.Schema != "" {
		cfg.SchemaMode = sc.Schema
	}
	if sc.MaxCalls != nil {
		cfg.MaxCalls = *sc.MaxCalls
	}
	if sc.Timeout != nil {
		cfg.TimeoutMs = *sc.Timeout
	}
	if sc.StateDir != "" {
		cfg.StateDir = sc.StateDir
	}
	if len(sc.Mask) > 0 {
		cfg.MaskPatterns = append(sc.Mask, cfg.MaskPatterns...)
	}
}
