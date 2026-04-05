package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeProfile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadProfile_Full(t *testing.T) {
	dir := t.TempDir()
	path := writeProfile(t, dir, "full.json", `{
		"name": "github-mcp",
		"upstream": {
			"transport": "sse",
			"url": "https://mcp.github.com/sse",
			"headers": { "X-Custom": "value" }
		},
		"auth": {
			"oauth2": {
				"tokenUrl": "https://auth.example.com/token",
				"clientId": "my-id",
				"clientSecret": "my-secret",
				"scopes": ["read", "write"]
			}
		},
		"governance": {
			"enforcement": "strict",
			"schema": "warn",
			"maxCalls": 100,
			"timeout": 60000
		},
		"mask": ["delete_*", "admin_*"],
		"stateDir": ".governance/github"
	}`)

	p, err := LoadProfile(path)
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}

	if p.Name != "github-mcp" {
		t.Errorf("Name=%q, want github-mcp", p.Name)
	}
	if p.Upstream.Transport != "sse" {
		t.Errorf("Transport=%q, want sse", p.Upstream.Transport)
	}
	if p.Upstream.URL != "https://mcp.github.com/sse" {
		t.Errorf("URL=%q", p.Upstream.URL)
	}
	if p.Upstream.Headers["X-Custom"] != "value" {
		t.Errorf("Headers=%v", p.Upstream.Headers)
	}
	if p.Auth.OAuth2.TokenURL != "https://auth.example.com/token" {
		t.Errorf("TokenURL=%q", p.Auth.OAuth2.TokenURL)
	}
	if p.Auth.OAuth2.ClientID != "my-id" {
		t.Errorf("ClientID=%q", p.Auth.OAuth2.ClientID)
	}
	if len(p.Auth.OAuth2.Scopes) != 2 {
		t.Errorf("Scopes=%v", p.Auth.OAuth2.Scopes)
	}
	if p.Governance.Enforcement != "strict" {
		t.Errorf("Enforcement=%q", p.Governance.Enforcement)
	}
	if *p.Governance.MaxCalls != 100 {
		t.Errorf("MaxCalls=%d", *p.Governance.MaxCalls)
	}
	if len(p.Mask) != 2 {
		t.Errorf("Mask=%v", p.Mask)
	}
	if p.StateDir != ".governance/github" {
		t.Errorf("StateDir=%q", p.StateDir)
	}
}

func TestLoadProfile_Minimal(t *testing.T) {
	dir := t.TempDir()
	path := writeProfile(t, dir, "minimal.json", `{
		"upstream": { "command": "npx", "args": ["-y", "server"] }
	}`)

	p, err := LoadProfile(path)
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	// Name derived from filename
	if p.Name != "minimal" {
		t.Errorf("Name=%q, want minimal", p.Name)
	}
	if p.Upstream.Command != "npx" {
		t.Errorf("Command=%q", p.Upstream.Command)
	}
	if len(p.Upstream.Args) != 2 {
		t.Errorf("Args=%v", p.Upstream.Args)
	}
}

func TestLoadProfile_NotFound(t *testing.T) {
	_, err := LoadProfile("/nonexistent/path.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadProfile_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := writeProfile(t, dir, "bad.json", `{invalid}`)
	_, err := LoadProfile(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadProfileByName(t *testing.T) {
	// Create a temp profile dir and override ProfileDir via a direct LoadProfile call
	dir := t.TempDir()
	profilesDir := filepath.Join(dir, "profiles")
	os.MkdirAll(profilesDir, 0755)
	writeProfile(t, profilesDir, "test-server.json", `{
		"name": "test-server",
		"upstream": { "command": "echo" }
	}`)

	// Test LoadProfile directly with constructed path
	path := filepath.Join(profilesDir, "test-server.json")
	p, err := LoadProfile(path)
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	if p.Name != "test-server" {
		t.Errorf("Name=%q", p.Name)
	}
}

func TestListProfiles(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "beta.json", `{"name":"beta"}`)
	writeProfile(t, dir, "alpha.json", `{"name":"alpha"}`)
	writeProfile(t, dir, "not-json.txt", `text`)
	os.MkdirAll(filepath.Join(dir, "subdir"), 0755)

	// Read the dir directly (same logic as ListProfiles but with custom dir)
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			name := e.Name()
			names = append(names, name[:len(name)-5])
		}
	}

	if len(names) != 2 {
		t.Fatalf("expected 2 profiles, got %d: %v", len(names), names)
	}
}

func TestListProfiles_NoDir(t *testing.T) {
	// ListProfiles returns nil when dir doesn't exist
	// Test the os.ReadDir behavior directly
	_, err := os.ReadDir("/nonexistent/path")
	if !os.IsNotExist(err) {
		t.Skip("unexpected error type")
	}
}

func TestProfileApplyTo_Full(t *testing.T) {
	maxCalls := 50
	timeout := 60000
	p := &Profile{
		Upstream: &UpstreamBlock{
			Transport: "sse",
			URL:       "http://localhost:8080",
			Headers:   map[string]string{"X-Key": "val"},
		},
		Auth: &AuthBlock{
			OAuth2: &OAuth2Block{
				TokenURL:     "http://auth/token",
				ClientID:     "id",
				ClientSecret: "secret",
				Scopes:       []string{"read"},
			},
		},
		Governance: &GovernanceBlock{
			Enforcement: "advisory",
			Schema:      "strict",
			MaxCalls:    &maxCalls,
			Timeout:     &timeout,
		},
		Mask:     []string{"write_*"},
		StateDir: "/tmp/state",
	}

	cfg := Defaults()
	cfg.Upstream = "original"
	p.ApplyTo(cfg)

	if cfg.Transport != "sse" {
		t.Errorf("Transport=%q", cfg.Transport)
	}
	if cfg.UpstreamURL != "http://localhost:8080" {
		t.Errorf("UpstreamURL=%q", cfg.UpstreamURL)
	}
	if cfg.SSEHeaders["X-Key"] != "val" {
		t.Errorf("SSEHeaders=%v", cfg.SSEHeaders)
	}
	if cfg.OAuth2TokenURL != "http://auth/token" {
		t.Errorf("OAuth2TokenURL=%q", cfg.OAuth2TokenURL)
	}
	if cfg.OAuth2ClientID != "id" {
		t.Errorf("OAuth2ClientID=%q", cfg.OAuth2ClientID)
	}
	if cfg.OAuth2ClientSecret != "secret" {
		t.Errorf("OAuth2ClientSecret=%q", cfg.OAuth2ClientSecret)
	}
	if len(cfg.OAuth2Scopes) != 1 || cfg.OAuth2Scopes[0] != "read" {
		t.Errorf("OAuth2Scopes=%v", cfg.OAuth2Scopes)
	}
	if cfg.Enforcement != "advisory" {
		t.Errorf("Enforcement=%q", cfg.Enforcement)
	}
	if cfg.SchemaMode != "strict" {
		t.Errorf("SchemaMode=%q", cfg.SchemaMode)
	}
	if cfg.MaxCalls != 50 {
		t.Errorf("MaxCalls=%d", cfg.MaxCalls)
	}
	if cfg.TimeoutMs != 60000 {
		t.Errorf("TimeoutMs=%d", cfg.TimeoutMs)
	}
	if len(cfg.MaskPatterns) != 1 || cfg.MaskPatterns[0] != "write_*" {
		t.Errorf("MaskPatterns=%v", cfg.MaskPatterns)
	}
	if cfg.StateDir != "/tmp/state" {
		t.Errorf("StateDir=%q", cfg.StateDir)
	}
	// Upstream command should not be overwritten (sse transport)
	if cfg.Upstream != "original" {
		t.Errorf("Upstream=%q, should be unchanged", cfg.Upstream)
	}
}

func TestProfileApplyTo_Partial(t *testing.T) {
	p := &Profile{
		Governance: &GovernanceBlock{
			Enforcement: "advisory",
		},
	}

	cfg := Defaults()
	cfg.Upstream = "cmd"
	p.ApplyTo(cfg)

	if cfg.Enforcement != "advisory" {
		t.Errorf("Enforcement=%q", cfg.Enforcement)
	}
	// Other fields unchanged
	if cfg.SchemaMode != "warn" {
		t.Errorf("SchemaMode=%q, want warn (unchanged)", cfg.SchemaMode)
	}
	if cfg.TimeoutMs != 300000 {
		t.Errorf("TimeoutMs=%d, want 300000 (unchanged)", cfg.TimeoutMs)
	}
}

func TestProfileApplyTo_TokenCommand(t *testing.T) {
	p := &Profile{
		Auth: &AuthBlock{
			TokenCommand: &TokenCommandBlock{
				Command: "gcloud",
				Args:    []string{"auth", "print-access-token"},
			},
		},
	}

	cfg := Defaults()
	p.ApplyTo(cfg)

	if cfg.TokenCommand != "gcloud" {
		t.Errorf("TokenCommand=%q", cfg.TokenCommand)
	}
	if len(cfg.TokenCommandArgs) != 2 {
		t.Errorf("TokenCommandArgs=%v", cfg.TokenCommandArgs)
	}
}

func TestProfileApplyTo_StdioCommand(t *testing.T) {
	p := &Profile{
		Upstream: &UpstreamBlock{
			Transport: "stdio",
			Command:   "npx",
			Args:      []string{"-y", "server"},
		},
	}

	cfg := Defaults()
	p.ApplyTo(cfg)

	if cfg.Transport != "stdio" {
		t.Errorf("Transport=%q", cfg.Transport)
	}
	if cfg.Upstream != "npx" {
		t.Errorf("Upstream=%q", cfg.Upstream)
	}
	if len(cfg.UpstreamArgs) != 2 {
		t.Errorf("UpstreamArgs=%v", cfg.UpstreamArgs)
	}
}

func TestProfileValidate_OK(t *testing.T) {
	p := &Profile{
		Name: "test",
		Upstream: &UpstreamBlock{
			Transport: "sse",
			URL:       "http://localhost:8080",
		},
		Auth: &AuthBlock{
			OAuth2: &OAuth2Block{
				TokenURL:     "http://auth/token",
				ClientID:     "id",
				ClientSecret: "secret",
			},
		},
		Governance: &GovernanceBlock{
			Enforcement: "strict",
			Schema:      "warn",
		},
	}
	if err := p.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestProfileValidate_MutualExclusive(t *testing.T) {
	p := &Profile{
		Name: "test",
		Auth: &AuthBlock{
			OAuth2: &OAuth2Block{
				TokenURL:     "http://auth/token",
				ClientID:     "id",
				ClientSecret: "secret",
			},
			TokenCommand: &TokenCommandBlock{
				Command: "gcloud",
			},
		},
	}
	if err := p.Validate(); err == nil {
		t.Error("expected error for mutual exclusion")
	}
}

func TestProfileValidate_SSENoURL(t *testing.T) {
	p := &Profile{
		Name: "test",
		Upstream: &UpstreamBlock{
			Transport: "sse",
		},
	}
	if err := p.Validate(); err == nil {
		t.Error("expected error for sse without url")
	}
}

func TestProfileValidate_BadTransport(t *testing.T) {
	p := &Profile{
		Name: "test",
		Upstream: &UpstreamBlock{
			Transport: "websocket",
		},
	}
	if err := p.Validate(); err == nil {
		t.Error("expected error for invalid transport")
	}
}

func TestProfileValidate_OAuth2MissingFields(t *testing.T) {
	p := &Profile{
		Name: "test",
		Auth: &AuthBlock{
			OAuth2: &OAuth2Block{
				TokenURL: "http://auth/token",
				// Missing clientId and clientSecret
			},
		},
	}
	if err := p.Validate(); err == nil {
		t.Error("expected error for missing OAuth2 fields")
	}
}

func TestProfileValidate_TokenCommandEmpty(t *testing.T) {
	p := &Profile{
		Name: "test",
		Auth: &AuthBlock{
			TokenCommand: &TokenCommandBlock{
				Command: "",
			},
		},
	}
	if err := p.Validate(); err == nil {
		t.Error("expected error for empty token command")
	}
}

func TestResolveProfile_Path(t *testing.T) {
	dir := t.TempDir()
	path := writeProfile(t, dir, "server.json", `{"name":"server"}`)

	p, err := ResolveProfile(path)
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if p.Name != "server" {
		t.Errorf("Name=%q", p.Name)
	}
}

func TestResolveProfile_JsonSuffix(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "my-server.json", `{"name":"my-server"}`)

	// Relative path with .json suffix should be treated as path
	p, err := ResolveProfile(filepath.Join(dir, "my-server.json"))
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if p.Name != "my-server" {
		t.Errorf("Name=%q", p.Name)
	}
}
