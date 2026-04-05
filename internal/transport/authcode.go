package transport

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const tokensFile = "tokens.json"

// StoredTokens holds OAuth2 tokens persisted to disk.
type StoredTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresAt    int64  `json:"expires_at"` // unix timestamp
}

// StoredTokenConfig holds settings for the stored token provider.
type StoredTokenConfig struct {
	StateDir     string
	TokenURL     string
	ClientID     string
	ClientSecret string // may be empty for public clients (PKCE)
}

// storedTokenProvider implements TokenProvider using tokens stored on disk.
// It automatically refreshes the access token using the refresh token when
// the access token expires.
type storedTokenProvider struct {
	cfg StoredTokenConfig

	mu     sync.Mutex
	tokens *StoredTokens
}

// NewStoredTokenProvider creates a TokenProvider that reads tokens from
// stateDir/tokens.json and refreshes them automatically.
// Returns an error if no stored tokens are found (run --login first).
func NewStoredTokenProvider(cfg StoredTokenConfig) (TokenProvider, error) {
	tokens, err := loadTokens(cfg.StateDir)
	if err != nil {
		return nil, fmt.Errorf("no stored tokens found (run --login first): %w", err)
	}

	// If tokenURL/clientID are empty, try to load from discovery cache
	if cfg.TokenURL == "" || cfg.ClientID == "" {
		if disc, err := loadDiscoveryCache(cfg.StateDir); err == nil {
			if cfg.TokenURL == "" {
				cfg.TokenURL = disc.TokenURL
			}
			if cfg.ClientID == "" {
				cfg.ClientID = disc.ClientID
			}
			if cfg.ClientSecret == "" {
				cfg.ClientSecret = disc.ClientSecret
			}
		}
	}

	if cfg.TokenURL == "" {
		return nil, fmt.Errorf("token URL not configured and no discovery cache found")
	}

	return &storedTokenProvider{
		cfg:    cfg,
		tokens: tokens,
	}, nil
}

// discoveryCache mirrors the structure saved by cli/discover.go.
type discoveryCache struct {
	TokenURL     string `json:"token_url"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
}

func loadDiscoveryCache(stateDir string) (*discoveryCache, error) {
	path := filepath.Join(stateDir, "oauth2-discovery.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var d discoveryCache
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func (p *storedTokenProvider) Token() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Return cached token if still valid (with 30s margin)
	if p.tokens.AccessToken != "" && time.Now().Add(30*time.Second).Before(time.Unix(p.tokens.ExpiresAt, 0)) {
		return p.tokens.AccessToken, nil
	}

	// Refresh using refresh token
	if p.tokens.RefreshToken == "" {
		return "", fmt.Errorf("access token expired and no refresh token available (run --login again)")
	}

	newTokens, err := refreshTokens(p.cfg, p.tokens.RefreshToken)
	if err != nil {
		return "", fmt.Errorf("token refresh failed: %w", err)
	}

	// Preserve refresh token if not returned in response
	if newTokens.RefreshToken == "" {
		newTokens.RefreshToken = p.tokens.RefreshToken
	}

	p.tokens = newTokens

	// Persist to disk
	if err := saveTokens(p.cfg.StateDir, newTokens); err != nil {
		// Non-fatal: log but continue
		fmt.Fprintf(os.Stderr, "mcp-guardian: warning: failed to save refreshed tokens: %v\n", err)
	}

	return p.tokens.AccessToken, nil
}

func (p *storedTokenProvider) Invalidate() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tokens.AccessToken = ""
	p.tokens.ExpiresAt = 0
}

// refreshTokens exchanges a refresh token for new tokens.
func refreshTokens(cfg StoredTokenConfig, refreshToken string) (*StoredTokens, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {cfg.ClientID},
	}
	if cfg.ClientSecret != "" {
		form.Set("client_secret", cfg.ClientSecret)
	}

	resp, err := http.PostForm(cfg.TokenURL, form)
	if err != nil {
		return nil, fmt.Errorf("refresh request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read refresh response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("refresh failed (HTTP %d): %s", resp.StatusCode, truncateBytes(body, 200))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse refresh response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("empty access_token in refresh response")
	}

	expiresAt := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Unix()
	if tokenResp.ExpiresIn <= 0 {
		expiresAt = time.Now().Add(1 * time.Hour).Unix()
	}

	return &StoredTokens{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    tokenResp.TokenType,
		ExpiresAt:    expiresAt,
	}, nil
}

// loadTokens reads stored tokens from stateDir/tokens.json.
func loadTokens(stateDir string) (*StoredTokens, error) {
	path := filepath.Join(stateDir, tokensFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tokens StoredTokens
	if err := json.Unmarshal(data, &tokens); err != nil {
		return nil, err
	}
	return &tokens, nil
}

// saveTokens writes tokens to stateDir/tokens.json atomically.
func saveTokens(stateDir string, tokens *StoredTokens) error {
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(tokens, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(stateDir, tokensFile)
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// SaveTokens is the exported version for use by the login command.
func SaveTokens(stateDir string, tokens *StoredTokens) error {
	return saveTokens(stateDir, tokens)
}
