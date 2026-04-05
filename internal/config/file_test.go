package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadGlobal_Full(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	content := `{
  "telemetry": {
    "otlp": {
      "endpoint": "http://otel:4318",
      "headers": {"Authorization": "Bearer secret"},
      "batchSize": 20,
      "batchTimeout": 3000
    },
    "webhooks": ["https://hooks.example.com/abc"]
  },
  "defaults": {
    "enforcement": "strict",
    "schema": "warn",
    "maxCalls": 100,
    "timeout": 60000
  }
}`
	os.WriteFile(path, []byte(content), 0644)

	gc, err := LoadGlobal(path)
	if err != nil {
		t.Fatal(err)
	}

	if gc.Telemetry == nil || gc.Telemetry.OTLP == nil {
		t.Fatal("telemetry.otlp should not be nil")
	}
	if gc.Telemetry.OTLP.Endpoint != "http://otel:4318" {
		t.Errorf("otlp.endpoint: got %s", gc.Telemetry.OTLP.Endpoint)
	}
	if gc.Telemetry.OTLP.Headers["Authorization"] != "Bearer secret" {
		t.Errorf("otlp.headers: got %v", gc.Telemetry.OTLP.Headers)
	}
	if gc.Telemetry.OTLP.BatchSize == nil || *gc.Telemetry.OTLP.BatchSize != 20 {
		t.Errorf("otlp.batchSize: got %v", gc.Telemetry.OTLP.BatchSize)
	}
	if len(gc.Telemetry.Webhooks) != 1 {
		t.Errorf("webhooks: got %v", gc.Telemetry.Webhooks)
	}
	if gc.Defaults == nil || gc.Defaults.Enforcement != "strict" {
		t.Errorf("defaults.enforcement: got %v", gc.Defaults)
	}
	if gc.Defaults.MaxCalls == nil || *gc.Defaults.MaxCalls != 100 {
		t.Errorf("defaults.maxCalls: got %v", gc.Defaults.MaxCalls)
	}
}

func TestLoadGlobal_Partial(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte(`{"telemetry": {"otlp": {"endpoint": "http://otel:4318"}}}`), 0644)

	gc, err := LoadGlobal(path)
	if err != nil {
		t.Fatal(err)
	}
	if gc.Telemetry.OTLP.Endpoint != "http://otel:4318" {
		t.Errorf("endpoint: got %s", gc.Telemetry.OTLP.Endpoint)
	}
	if gc.Defaults != nil {
		t.Errorf("defaults should be nil, got %v", gc.Defaults)
	}
}

func TestLoadGlobal_NotFound(t *testing.T) {
	_, err := LoadGlobal("/nonexistent.json")
	if err == nil {
		t.Error("should return error")
	}
}

func TestGlobalConfig_ApplyTo(t *testing.T) {
	cfg := Defaults()

	maxCalls := 50
	timeout := 10000
	batchSize := 25
	batchTimeout := 2000
	gc := &GlobalConfig{
		Telemetry: &TelemetryBlock{
			OTLP: &OTLPConfig{
				Endpoint:     "http://otel:4318",
				Headers:      map[string]string{"X-Key": "val"},
				BatchSize:    &batchSize,
				BatchTimeout: &batchTimeout,
			},
			Webhooks: []string{"http://hook1"},
		},
		Defaults: &DefaultsBlock{
			Enforcement: "advisory",
			Schema:      "strict",
			MaxCalls:    &maxCalls,
			Timeout:     &timeout,
		},
	}

	gc.ApplyTo(cfg)

	if cfg.Enforcement != "advisory" {
		t.Errorf("enforcement: got %s", cfg.Enforcement)
	}
	if cfg.SchemaMode != "strict" {
		t.Errorf("schemaMode: got %s", cfg.SchemaMode)
	}
	if cfg.MaxCalls != 50 {
		t.Errorf("maxCalls: got %d", cfg.MaxCalls)
	}
	if cfg.TimeoutMs != 10000 {
		t.Errorf("timeout: got %d", cfg.TimeoutMs)
	}
	if cfg.OTLPEndpoint != "http://otel:4318" {
		t.Errorf("otlp endpoint: got %s", cfg.OTLPEndpoint)
	}
	if cfg.OTLPHeaders["X-Key"] != "val" {
		t.Errorf("otlp headers: got %v", cfg.OTLPHeaders)
	}
	if cfg.OTLPBatchSize != 25 {
		t.Errorf("otlp batchSize: got %d", cfg.OTLPBatchSize)
	}
	if len(cfg.WebhookURLs) != 1 || cfg.WebhookURLs[0] != "http://hook1" {
		t.Errorf("webhooks: got %v", cfg.WebhookURLs)
	}
}

func TestGlobalConfig_NoTelemetry(t *testing.T) {
	cfg := Defaults()
	gc := &GlobalConfig{
		Defaults: &DefaultsBlock{Enforcement: "advisory"},
	}
	gc.ApplyTo(cfg)

	if cfg.Enforcement != "advisory" {
		t.Errorf("enforcement: got %s", cfg.Enforcement)
	}
	if cfg.OTLPEndpoint != "" {
		t.Errorf("otlp should be empty, got %s", cfg.OTLPEndpoint)
	}
}

func TestApplyOrder_GlobalThenProfile(t *testing.T) {
	cfg := Defaults()

	gc := &GlobalConfig{
		Telemetry: &TelemetryBlock{
			OTLP: &OTLPConfig{Endpoint: "http://otel:4318"},
		},
		Defaults: &DefaultsBlock{
			Enforcement: "strict",
			Schema:      "warn",
		},
	}
	gc.ApplyTo(cfg)

	maxCalls := 100
	p := &Profile{
		Name: "test",
		Governance: &GovernanceBlock{
			Enforcement: "advisory",
			MaxCalls:    &maxCalls,
		},
		Mask: []string{"admin_*"},
	}
	p.ApplyTo(cfg)

	if cfg.Enforcement != "advisory" {
		t.Errorf("Enforcement=%q, want advisory (profile overrides global)", cfg.Enforcement)
	}
	if cfg.SchemaMode != "warn" {
		t.Errorf("SchemaMode=%q, want warn (from global)", cfg.SchemaMode)
	}
	if cfg.OTLPEndpoint != "http://otel:4318" {
		t.Errorf("OTLPEndpoint=%q, want http://otel:4318 (from global)", cfg.OTLPEndpoint)
	}
	if cfg.MaxCalls != 100 {
		t.Errorf("MaxCalls=%d, want 100", cfg.MaxCalls)
	}
	if len(cfg.MaskPatterns) != 1 || cfg.MaskPatterns[0] != "admin_*" {
		t.Errorf("MaskPatterns=%v", cfg.MaskPatterns)
	}
}
