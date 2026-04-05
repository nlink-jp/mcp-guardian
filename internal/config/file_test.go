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
  "otlp": {
    "endpoint": "http://otel:4318",
    "headers": {"Authorization": "Bearer secret"},
    "batchSize": 20,
    "batchTimeout": 3000
  },
  "webhooks": ["https://hooks.example.com/abc"],
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

	if gc.OTLP == nil || gc.OTLP.Endpoint != "http://otel:4318" {
		t.Errorf("otlp.endpoint: got %v", gc.OTLP)
	}
	if gc.OTLP.Headers["Authorization"] != "Bearer secret" {
		t.Errorf("otlp.headers: got %v", gc.OTLP.Headers)
	}
	if gc.OTLP.BatchSize == nil || *gc.OTLP.BatchSize != 20 {
		t.Errorf("otlp.batchSize: got %v", gc.OTLP.BatchSize)
	}
	if len(gc.Webhooks) != 1 {
		t.Errorf("webhooks: got %v", gc.Webhooks)
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
	os.WriteFile(path, []byte(`{"otlp": {"endpoint": "http://otel:4318"}}`), 0644)

	gc, err := LoadGlobal(path)
	if err != nil {
		t.Fatal(err)
	}
	if gc.OTLP.Endpoint != "http://otel:4318" {
		t.Errorf("endpoint: got %s", gc.OTLP.Endpoint)
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
		OTLP: &OTLPConfig{
			Endpoint:     "http://otel:4318",
			Headers:      map[string]string{"X-Key": "val"},
			BatchSize:    &batchSize,
			BatchTimeout: &batchTimeout,
		},
		Webhooks: []string{"http://hook1"},
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

func TestLoadServer_Full(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.json")

	content := `{
  "enforcement": "advisory",
  "schema": "strict",
  "maxCalls": 50,
  "timeout": 60000,
  "stateDir": "/var/lib/guardian",
  "mask": ["write_*", "delete_*"]
}`
	os.WriteFile(path, []byte(content), 0644)

	sc, err := LoadServer(path)
	if err != nil {
		t.Fatal(err)
	}

	if sc.Enforcement != "advisory" {
		t.Errorf("enforcement: got %s", sc.Enforcement)
	}
	if sc.MaxCalls == nil || *sc.MaxCalls != 50 {
		t.Errorf("maxCalls: got %v", sc.MaxCalls)
	}
	if len(sc.Mask) != 2 || sc.Mask[0] != "write_*" {
		t.Errorf("mask: got %v", sc.Mask)
	}
}

func TestLoadServer_NotFound(t *testing.T) {
	_, err := LoadServer("/nonexistent.json")
	if err == nil {
		t.Error("should return error")
	}
}

func TestServerConfig_ApplyTo(t *testing.T) {
	cfg := Defaults()

	maxCalls := 50
	sc := &ServerConfig{
		Enforcement: "advisory",
		Schema:      "strict",
		MaxCalls:    &maxCalls,
		StateDir:    "/custom/state",
		Mask:        []string{"file_*"},
	}

	sc.ApplyTo(cfg)

	if cfg.Enforcement != "advisory" {
		t.Errorf("enforcement: got %s", cfg.Enforcement)
	}
	if cfg.SchemaMode != "strict" {
		t.Errorf("schemaMode: got %s", cfg.SchemaMode)
	}
	if cfg.MaxCalls != 50 {
		t.Errorf("maxCalls: got %d", cfg.MaxCalls)
	}
	if cfg.StateDir != "/custom/state" {
		t.Errorf("stateDir: got %s", cfg.StateDir)
	}
	if len(cfg.MaskPatterns) != 1 || cfg.MaskPatterns[0] != "file_*" {
		t.Errorf("mask: got %v", cfg.MaskPatterns)
	}
	// Should NOT touch OTLP
	if cfg.OTLPEndpoint != "" {
		t.Errorf("server config should not set OTLP, got %s", cfg.OTLPEndpoint)
	}
}

func TestServerConfig_MaskMerge(t *testing.T) {
	cfg := Defaults()
	cfg.MaskPatterns = []string{"cli_pattern"}

	sc := &ServerConfig{
		Mask: []string{"server_pattern"},
	}
	sc.ApplyTo(cfg)

	if len(cfg.MaskPatterns) != 2 {
		t.Fatalf("expected 2 mask patterns, got %d: %v", len(cfg.MaskPatterns), cfg.MaskPatterns)
	}
	if cfg.MaskPatterns[0] != "server_pattern" || cfg.MaskPatterns[1] != "cli_pattern" {
		t.Errorf("mask order should be [server, cli], got %v", cfg.MaskPatterns)
	}
}

func TestApplyOrder_GlobalThenServer(t *testing.T) {
	cfg := Defaults()

	// Global sets defaults
	gc := &GlobalConfig{
		OTLP: &OTLPConfig{Endpoint: "http://otel:4318"},
		Defaults: &DefaultsBlock{
			Enforcement: "strict",
			Schema:      "warn",
		},
	}
	gc.ApplyTo(cfg)

	if cfg.Enforcement != "strict" {
		t.Errorf("after global: enforcement should be strict, got %s", cfg.Enforcement)
	}

	// Server overrides enforcement
	sc := &ServerConfig{
		Enforcement: "advisory",
		Mask:        []string{"exec_*"},
	}
	sc.ApplyTo(cfg)

	if cfg.Enforcement != "advisory" {
		t.Errorf("after server: enforcement should be advisory, got %s", cfg.Enforcement)
	}
	if cfg.SchemaMode != "warn" {
		t.Errorf("schema should remain from global defaults, got %s", cfg.SchemaMode)
	}
	if cfg.OTLPEndpoint != "http://otel:4318" {
		t.Errorf("OTLP should remain from global, got %s", cfg.OTLPEndpoint)
	}
	if len(cfg.MaskPatterns) != 1 || cfg.MaskPatterns[0] != "exec_*" {
		t.Errorf("mask: got %v", cfg.MaskPatterns)
	}
}

func TestServerConfig_MaxCallsZero(t *testing.T) {
	cfg := Defaults()
	cfg.MaxCalls = 100

	zero := 0
	sc := &ServerConfig{MaxCalls: &zero}
	sc.ApplyTo(cfg)

	if cfg.MaxCalls != 0 {
		t.Errorf("maxCalls should be overwritten to 0, got %d", cfg.MaxCalls)
	}
}

func TestServerConfig_PartialNoOverwrite(t *testing.T) {
	cfg := Defaults()
	sc := &ServerConfig{}
	sc.ApplyTo(cfg)

	defaults := Defaults()
	if cfg.Enforcement != defaults.Enforcement {
		t.Errorf("enforcement should remain default, got %s", cfg.Enforcement)
	}
	if cfg.TimeoutMs != defaults.TimeoutMs {
		t.Errorf("timeout should remain default, got %d", cfg.TimeoutMs)
	}
}
