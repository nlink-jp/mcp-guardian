package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
	"os"
	"path/filepath"
	"strings"
)

const discoveryFile = "oauth2-discovery.json"

var discoveryHTTPClient = &http.Client{Timeout: 30 * time.Second}

// DiscoveredOAuth2 holds auto-discovered OAuth2 configuration
// from MCP Authorization Discovery (RFC 8414 + Dynamic Client Registration).
type DiscoveredOAuth2 struct {
	AuthorizeURL string   `json:"authorize_url"`
	TokenURL     string   `json:"token_url"`
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
}

// DiscoverOAuth2 performs MCP Authorization Discovery:
//  1. POST to MCP server → expect 401 with resource_metadata URL
//  2. Fetch resource metadata → get authorization server URL
//  3. Fetch authorization server metadata → get endpoints
//  4. Dynamic Client Registration → get client_id
//
// Results are saved to stateDir for reuse.
func DiscoverOAuth2(mcpURL, stateDir, redirectURI string) (*DiscoveredOAuth2, error) {
	fmt.Println("Discovering OAuth2 configuration from MCP server...")

	// Try two discovery strategies for finding auth server metadata:
	// 1. MCP spec: 401 → resource_metadata → auth server
	// 2. Fallback: .well-known/oauth-authorization-server on MCP host
	var authMeta *authServerMetadata

	resourceMetadataURL, err := probeForAuth(mcpURL)
	if err == nil {
		authServerURL, err := fetchResourceMetadata(resourceMetadataURL)
		if err == nil {
			authMeta, err = fetchAuthServerMetadata(authServerURL)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: auth server metadata failed: %v\n", err)
			}
		} else {
			fmt.Fprintf(os.Stderr, "warning: resource metadata failed: %v\n", err)
		}
	} else {
		fmt.Fprintf(os.Stderr, "note: no resource_metadata in 401 response, trying fallback discovery\n")
	}

	if authMeta == nil {
		parsed, err := url.Parse(mcpURL)
		if err != nil {
			return nil, fmt.Errorf("parse MCP URL: %w", err)
		}
		baseURL := fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
		authMeta, err = fetchAuthServerMetadata(baseURL)
		if err != nil {
			return nil, fmt.Errorf("fallback auth discovery failed: %w", err)
		}
	}

	// Dynamic Client Registration — always re-register because
	// redirect_uri includes a dynamic port that changes each run
	clientID, clientSecret, err := registerClient(authMeta, redirectURI)
	if err != nil {
		return nil, fmt.Errorf("client registration: %w", err)
	}

	discovered := &DiscoveredOAuth2{
		AuthorizeURL: authMeta.AuthorizationEndpoint,
		TokenURL:     authMeta.TokenEndpoint,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Scopes:       authMeta.ScopesSupported,
	}

	// Save for reuse
	if err := saveDiscovery(stateDir, discovered); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to cache discovery: %v\n", err)
	}

	fmt.Printf("OAuth2 discovery complete.\n")
	fmt.Printf("  Authorization: %s\n", discovered.AuthorizeURL)
	fmt.Printf("  Token:         %s\n", discovered.TokenURL)
	fmt.Printf("  Client ID:     %s\n", discovered.ClientID)

	return discovered, nil
}

// probeForAuth sends a request to the MCP server and extracts
// the resource_metadata URL from the 401 WWW-Authenticate header.
// Returns ("", nil) if the server returns 401 but without resource_metadata
// (allowing fallback to .well-known discovery).
func probeForAuth(mcpURL string) (string, error) {
	// Send a minimal JSON-RPC request to trigger auth
	body := strings.NewReader(`{"jsonrpc":"2.0","id":"_probe","method":"initialize","params":{}}`)
	req, err := http.NewRequest("POST", mcpURL, body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := discoveryHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("connect to %s: %w", mcpURL, err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		return "", fmt.Errorf("expected 401 from MCP server, got %d (server may not require auth)", resp.StatusCode)
	}

	// Parse WWW-Authenticate header for resource_metadata URL
	wwwAuth := resp.Header.Get("WWW-Authenticate")
	metadataURL := extractParam(wwwAuth, "resource_metadata")
	if metadataURL == "" {
		// No resource_metadata — caller should try fallback discovery
		return "", fmt.Errorf("no resource_metadata in WWW-Authenticate: %s", wwwAuth)
	}

	return metadataURL, nil
}

// authServerMetadata holds the OAuth 2.0 Authorization Server Metadata (RFC 8414).
type authServerMetadata struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	RegistrationEndpoint  string   `json:"registration_endpoint"`
	ScopesSupported       []string `json:"scopes_supported"`
	CodeChallengeMethods  []string `json:"code_challenge_methods_supported"`
}

// fetchResourceMetadata fetches the Protected Resource Metadata
// and returns the authorization server URL.
func fetchResourceMetadata(metadataURL string) (string, error) {
	resp, err := discoveryHTTPClient.Get(metadataURL)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", metadataURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncStr(string(body), 200))
	}

	var meta struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		return "", fmt.Errorf("parse resource metadata: %w", err)
	}

	if len(meta.AuthorizationServers) == 0 {
		return "", fmt.Errorf("no authorization_servers in resource metadata")
	}

	return meta.AuthorizationServers[0], nil
}

// fetchAuthServerMetadata fetches OAuth 2.0 Authorization Server Metadata
// from the well-known endpoint.
func fetchAuthServerMetadata(authServerURL string) (*authServerMetadata, error) {
	// Build well-known URL
	parsed, err := url.Parse(authServerURL)
	if err != nil {
		return nil, err
	}
	wellKnown := fmt.Sprintf("%s://%s/.well-known/oauth-authorization-server", parsed.Scheme, parsed.Host)
	if parsed.Path != "" && parsed.Path != "/" {
		wellKnown = fmt.Sprintf("%s://%s/.well-known/oauth-authorization-server%s", parsed.Scheme, parsed.Host, parsed.Path)
	}

	resp, err := discoveryHTTPClient.Get(wellKnown)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", wellKnown, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, wellKnown, truncStr(string(body), 200))
	}

	var meta authServerMetadata
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, fmt.Errorf("parse auth server metadata: %w", err)
	}

	if meta.AuthorizationEndpoint == "" || meta.TokenEndpoint == "" {
		return nil, fmt.Errorf("auth server metadata missing required endpoints")
	}

	return &meta, nil
}

// registerClient performs OAuth 2.0 Dynamic Client Registration (RFC 7591).
// The redirectURI must match the actual callback server address (including port).
func registerClient(meta *authServerMetadata, redirectURI string) (clientID, clientSecret string, err error) {
	if meta.RegistrationEndpoint == "" {
		return "", "", fmt.Errorf("authorization server does not support dynamic client registration")
	}

	regReq := map[string]interface{}{
		"client_name":                "mcp-guardian",
		"redirect_uris":             []string{redirectURI},
		"grant_types":               []string{"authorization_code", "refresh_token"},
		"response_types":            []string{"code"},
		"token_endpoint_auth_method": "none",
	}

	reqBody, err := json.Marshal(regReq)
	if err != nil {
		return "", "", err
	}

	resp, err := discoveryHTTPClient.Post(meta.RegistrationEndpoint, "application/json", strings.NewReader(string(reqBody)))
	if err != nil {
		return "", "", fmt.Errorf("registration request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", "", err
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", "", fmt.Errorf("registration failed (HTTP %d): %s", resp.StatusCode, truncStr(string(body), 200))
	}

	var regResp struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.Unmarshal(body, &regResp); err != nil {
		return "", "", fmt.Errorf("parse registration response: %w", err)
	}

	if regResp.ClientID == "" {
		return "", "", fmt.Errorf("empty client_id in registration response")
	}

	return regResp.ClientID, regResp.ClientSecret, nil
}

// extractParam extracts a quoted parameter value from a WWW-Authenticate header.
// e.g., extractParam(`Bearer resource_metadata="https://..."`, "resource_metadata")
func extractParam(header, param string) string {
	key := param + "=\""
	idx := strings.Index(header, key)
	if idx < 0 {
		return ""
	}
	start := idx + len(key)
	end := strings.Index(header[start:], "\"")
	if end < 0 {
		return ""
	}
	return header[start : start+end]
}

func truncStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func loadDiscovery(stateDir string) (*DiscoveredOAuth2, error) {
	path := filepath.Join(stateDir, discoveryFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var d DiscoveredOAuth2
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func saveDiscovery(stateDir string, d *DiscoveredOAuth2) error {
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(stateDir, discoveryFile)
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
