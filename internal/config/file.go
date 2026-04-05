package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// GlobalConfig represents the system-wide configuration file.
// Contains telemetry settings and default values for all MCP server instances.
//
// Format:
//
//	{ "telemetry": { "otlp": {...}, "webhooks": [...] }, "defaults": {...} }
type GlobalConfig struct {
	Telemetry *TelemetryBlock `json:"telemetry,omitempty"`
	Defaults  *DefaultsBlock  `json:"defaults,omitempty"`
}

// TelemetryBlock groups telemetry settings under a single key.
type TelemetryBlock struct {
	OTLP     *OTLPConfig    `json:"otlp,omitempty"`
	Splunk   *SplunkConfig  `json:"splunk,omitempty"`
	Webhooks []string       `json:"webhooks,omitempty"`
}

// SplunkConfig holds Splunk HEC export settings.
type SplunkConfig struct {
	Endpoint     string `json:"endpoint"`
	Token        string `json:"token"`
	Index        string `json:"index,omitempty"`
	BatchSize    *int   `json:"batchSize,omitempty"`
	BatchTimeout *int   `json:"batchTimeout,omitempty"`
}

// DefaultsBlock holds default values for per-server settings.
type DefaultsBlock struct {
	Enforcement       string `json:"enforcement,omitempty"`
	Schema            string `json:"schema,omitempty"`
	MaxCalls          *int   `json:"maxCalls,omitempty"`
	Timeout           *int   `json:"timeout,omitempty"`
	MaxReceiptAgeDays *int   `json:"maxReceiptAgeDays,omitempty"`
}

// OTLPConfig holds OTLP export settings.
type OTLPConfig struct {
	Endpoint     string            `json:"endpoint,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	BatchSize    *int              `json:"batchSize,omitempty"`
	BatchTimeout *int              `json:"batchTimeout,omitempty"`
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

// GlobalConfigDir returns the default global config directory path.
func GlobalConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "mcp-guardian")
}

// GlobalConfigPath returns the default global config file path.
func GlobalConfigPath() string {
	dir := GlobalConfigDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "config.json")
}

// LoadGlobalAuto loads the global config from the default path if it exists.
// Returns (nil, nil) when the file does not exist.
func LoadGlobalAuto() (*GlobalConfig, error) {
	path := GlobalConfigPath()
	if path == "" {
		return nil, nil
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, nil
	}
	return LoadGlobal(path)
}

// ApplyTo merges global config into a Config.
// Applies defaults block as base values, plus telemetry (OTLP and webhooks).
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
		if gc.Defaults.MaxReceiptAgeDays != nil {
			cfg.MaxReceiptAgeDays = *gc.Defaults.MaxReceiptAgeDays
		}
	}
	if gc.Telemetry != nil {
		if len(gc.Telemetry.Webhooks) > 0 {
			cfg.WebhookURLs = append(gc.Telemetry.Webhooks, cfg.WebhookURLs...)
		}
		otlp := gc.Telemetry.OTLP
		if otlp != nil {
			if otlp.Endpoint != "" {
				cfg.OTLPEndpoint = otlp.Endpoint
			}
			if len(otlp.Headers) > 0 {
				if cfg.OTLPHeaders == nil {
					cfg.OTLPHeaders = make(map[string]string)
				}
				for k, v := range otlp.Headers {
					cfg.OTLPHeaders[k] = v
				}
			}
			if otlp.BatchSize != nil {
				cfg.OTLPBatchSize = *otlp.BatchSize
			}
			if otlp.BatchTimeout != nil {
				cfg.OTLPBatchTimeout = *otlp.BatchTimeout
			}
		}
		splunk := gc.Telemetry.Splunk
		if splunk != nil {
			if splunk.Endpoint != "" {
				cfg.SplunkHECEndpoint = splunk.Endpoint
			}
			if splunk.Token != "" {
				cfg.SplunkHECToken = splunk.Token
			}
			if splunk.Index != "" {
				cfg.SplunkHECIndex = splunk.Index
			}
			if splunk.BatchSize != nil {
				cfg.SplunkHECBatchSize = *splunk.BatchSize
			}
			if splunk.BatchTimeout != nil {
				cfg.SplunkHECBatchTimeout = *splunk.BatchTimeout
			}
		}
	}
}
