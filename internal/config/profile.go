package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Profile represents a named per-MCP-server configuration.
// Each profile defines everything needed to connect to and govern a single
// MCP server: transport, authentication, governance policy, and masking.
//
// Telemetry settings (OTLP, webhooks) belong in the system global config,
// not in profiles.
type Profile struct {
	Name       string           `json:"name"`
	Upstream   *UpstreamBlock   `json:"upstream,omitempty"`
	Auth       *AuthBlock       `json:"auth,omitempty"`
	Governance *GovernanceBlock `json:"governance,omitempty"`
	Mask       []string         `json:"mask,omitempty"`
	StateDir   string           `json:"stateDir,omitempty"`
}

// UpstreamBlock defines how to connect to the MCP server.
type UpstreamBlock struct {
	Transport string            `json:"transport,omitempty"` // "stdio" or "sse"
	URL       string            `json:"url,omitempty"`       // MCP server URL (sse)
	Command   string            `json:"command,omitempty"`   // binary to execute (stdio)
	Args      []string          `json:"args,omitempty"`      // command arguments (stdio)
	Headers   map[string]string `json:"headers,omitempty"`   // HTTP headers (sse)
}

// AuthBlock defines authentication for the upstream MCP server.
// OAuth2 and TokenCommand are mutually exclusive.
type AuthBlock struct {
	OAuth2       *OAuth2Block       `json:"oauth2,omitempty"`
	TokenCommand *TokenCommandBlock `json:"tokenCommand,omitempty"`
}

// OAuth2Block holds OAuth2 settings.
// Supports two flows:
//   - "client_credentials" (default): M2M, no browser. Requires clientSecret.
//   - "authorization_code": Browser login via --login. Uses PKCE, clientSecret optional.
type OAuth2Block struct {
	Flow         string            `json:"flow,omitempty"`         // "client_credentials" (default) or "authorization_code"
	AuthorizeURL string            `json:"authorizeUrl,omitempty"` // authorization endpoint (authorization_code only)
	TokenURL     string            `json:"tokenUrl"`
	ClientID     string            `json:"clientId"`
	ClientSecret string            `json:"clientSecret,omitempty"` // required for client_credentials, optional for authorization_code
	Scopes       []string          `json:"scopes,omitempty"`
	ExtraParams  map[string]string `json:"extraParams,omitempty"`  // additional authorize URL params (e.g., audience, prompt)
}

// TokenCommandBlock holds external token command settings.
type TokenCommandBlock struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

// GovernanceBlock holds governance policy settings.
type GovernanceBlock struct {
	Enforcement       string `json:"enforcement,omitempty"` // "strict" or "advisory"
	Schema            string `json:"schema,omitempty"`      // "off", "warn", or "strict"
	MaxCalls          *int   `json:"maxCalls,omitempty"`
	Timeout           *int   `json:"timeout,omitempty"`
	MaxReceiptAgeDays *int   `json:"maxReceiptAgeDays,omitempty"` // 0 = no purge
}

// ProfileDir returns the default profile directory path.
func ProfileDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "mcp-guardian", "profiles")
}

// LoadProfile reads and parses a profile from the given file path.
func LoadProfile(path string) (*Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read profile: %w", err)
	}
	var p Profile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse profile: %w", err)
	}
	// Set name from filename if not specified in JSON
	if p.Name == "" {
		base := filepath.Base(path)
		p.Name = strings.TrimSuffix(base, filepath.Ext(base))
	}
	return &p, nil
}

// LoadProfileByName loads a profile by name from the default profile directory.
func LoadProfileByName(name string) (*Profile, error) {
	dir := ProfileDir()
	if dir == "" {
		return nil, fmt.Errorf("cannot determine profile directory")
	}
	path := filepath.Join(dir, name+".json")
	return LoadProfile(path)
}

// ResolveProfile loads a profile from a name or path.
// If the value contains a path separator or ends in ".json", it is treated
// as a file path. Otherwise, it is treated as a profile name.
func ResolveProfile(nameOrPath string) (*Profile, error) {
	if strings.ContainsRune(nameOrPath, filepath.Separator) ||
		strings.HasSuffix(nameOrPath, ".json") {
		return LoadProfile(nameOrPath)
	}
	return LoadProfileByName(nameOrPath)
}

// ListProfiles returns the names of all profiles in the default profile
// directory, sorted alphabetically. Returns an empty slice if the directory
// does not exist.
func ListProfiles() ([]string, error) {
	dir := ProfileDir()
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read profile directory: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".json") {
			names = append(names, strings.TrimSuffix(name, ".json"))
		}
	}
	sort.Strings(names)
	return names, nil
}

// Validate checks that the profile settings are internally consistent.
func (p *Profile) Validate() error {
	if p.Upstream != nil {
		switch p.Upstream.Transport {
		case "", "stdio", "sse":
		default:
			return fmt.Errorf("profile %q: transport must be 'stdio' or 'sse', got '%s'", p.Name, p.Upstream.Transport)
		}
		if p.Upstream.Transport == "sse" && p.Upstream.URL == "" {
			return fmt.Errorf("profile %q: upstream.url is required for sse transport", p.Name)
		}
	}
	if p.Auth != nil {
		if p.Auth.OAuth2 != nil && p.Auth.TokenCommand != nil {
			return fmt.Errorf("profile %q: auth.oauth2 and auth.tokenCommand are mutually exclusive", p.Name)
		}
		if p.Auth.OAuth2 != nil {
			switch p.Auth.OAuth2.Flow {
			case "", "client_credentials":
				if p.Auth.OAuth2.TokenURL == "" {
					return fmt.Errorf("profile %q: auth.oauth2.tokenUrl is required", p.Name)
				}
				if p.Auth.OAuth2.ClientID == "" || p.Auth.OAuth2.ClientSecret == "" {
					return fmt.Errorf("profile %q: auth.oauth2.clientId and clientSecret are required for client_credentials", p.Name)
				}
			case "authorization_code":
				if p.Auth.OAuth2.AuthorizeURL == "" {
					return fmt.Errorf("profile %q: auth.oauth2.authorizeUrl is required for authorization_code flow", p.Name)
				}
				if p.Auth.OAuth2.TokenURL == "" {
					return fmt.Errorf("profile %q: auth.oauth2.tokenUrl is required", p.Name)
				}
				if p.Auth.OAuth2.ClientID == "" {
					return fmt.Errorf("profile %q: auth.oauth2.clientId is required", p.Name)
				}
			default:
				return fmt.Errorf("profile %q: auth.oauth2.flow must be 'client_credentials' or 'authorization_code'", p.Name)
			}
		}
		if p.Auth.TokenCommand != nil {
			if p.Auth.TokenCommand.Command == "" {
				return fmt.Errorf("profile %q: auth.tokenCommand.command is required", p.Name)
			}
		}
	}
	if p.Governance != nil {
		if p.Governance.Enforcement != "" {
			switch p.Governance.Enforcement {
			case "strict", "advisory":
			default:
				return fmt.Errorf("profile %q: governance.enforcement must be 'strict' or 'advisory'", p.Name)
			}
		}
		if p.Governance.Schema != "" {
			switch p.Governance.Schema {
			case "off", "warn", "strict":
			default:
				return fmt.Errorf("profile %q: governance.schema must be 'off', 'warn', or 'strict'", p.Name)
			}
		}
		if p.Governance.MaxCalls != nil && *p.Governance.MaxCalls < 0 {
			return fmt.Errorf("profile %q: governance.maxCalls must be >= 0", p.Name)
		}
		if p.Governance.Timeout != nil && *p.Governance.Timeout <= 0 {
			return fmt.Errorf("profile %q: governance.timeout must be > 0", p.Name)
		}
	}
	return nil
}

// ApplyTo merges the profile settings into a Config.
// Only non-zero/non-nil values are applied.
func (p *Profile) ApplyTo(cfg *Config) {
	if p.Upstream != nil {
		if p.Upstream.Transport != "" {
			cfg.Transport = p.Upstream.Transport
		}
		if p.Upstream.URL != "" {
			cfg.UpstreamURL = p.Upstream.URL
		}
		if p.Upstream.Command != "" {
			cfg.Upstream = p.Upstream.Command
			cfg.UpstreamArgs = p.Upstream.Args
		}
		if len(p.Upstream.Headers) > 0 {
			if cfg.SSEHeaders == nil {
				cfg.SSEHeaders = make(map[string]string)
			}
			for k, v := range p.Upstream.Headers {
				cfg.SSEHeaders[k] = v
			}
		}
	}

	if p.Auth != nil {
		if p.Auth.OAuth2 != nil {
			cfg.OAuth2Flow = p.Auth.OAuth2.Flow
			cfg.OAuth2TokenURL = p.Auth.OAuth2.TokenURL
			cfg.OAuth2ClientID = p.Auth.OAuth2.ClientID
			cfg.OAuth2ClientSecret = p.Auth.OAuth2.ClientSecret
			cfg.OAuth2Scopes = p.Auth.OAuth2.Scopes
		}
		if p.Auth.TokenCommand != nil {
			cfg.TokenCommand = p.Auth.TokenCommand.Command
			cfg.TokenCommandArgs = p.Auth.TokenCommand.Args
		}
	}

	if p.Governance != nil {
		if p.Governance.Enforcement != "" {
			cfg.Enforcement = p.Governance.Enforcement
		}
		if p.Governance.Schema != "" {
			cfg.SchemaMode = p.Governance.Schema
		}
		if p.Governance.MaxCalls != nil {
			cfg.MaxCalls = *p.Governance.MaxCalls
		}
		if p.Governance.Timeout != nil {
			cfg.TimeoutMs = *p.Governance.Timeout
		}
		if p.Governance.MaxReceiptAgeDays != nil {
			cfg.MaxReceiptAgeDays = *p.Governance.MaxReceiptAgeDays
		}
	}

	if len(p.Mask) > 0 {
		cfg.MaskPatterns = append(p.Mask, cfg.MaskPatterns...)
	}

	if p.StateDir != "" {
		cfg.StateDir = p.StateDir
	}
}
