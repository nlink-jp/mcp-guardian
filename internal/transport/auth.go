package transport

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// TokenProvider supplies Bearer tokens for authenticated HTTP requests.
// Implementations must be safe for concurrent use.
type TokenProvider interface {
	// Token returns a valid access token.
	// Implementations should cache tokens and handle refresh internally.
	Token() (string, error)

	// Invalidate marks the current token as expired, forcing the next
	// call to Token() to fetch a fresh one. This is called when the
	// server responds with 401 Unauthorized.
	Invalidate()
}

// --- OAuth2 Client Credentials ---

// OAuth2Config holds settings for the OAuth2 client_credentials grant.
type OAuth2Config struct {
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scopes       []string
}

type oauth2Provider struct {
	cfg OAuth2Config

	mu          sync.Mutex
	accessToken string
	expiry      time.Time
}

// NewOAuth2Provider creates a TokenProvider that uses the OAuth2
// client_credentials grant type. Tokens are cached and automatically
// refreshed when they expire.
func NewOAuth2Provider(cfg OAuth2Config) TokenProvider {
	return &oauth2Provider{cfg: cfg}
}

func (p *oauth2Provider) Token() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Return cached token if still valid (with 30s safety margin)
	if p.accessToken != "" && time.Now().Add(30*time.Second).Before(p.expiry) {
		return p.accessToken, nil
	}

	// Fetch new token
	token, expiry, err := p.fetchToken()
	if err != nil {
		return "", err
	}

	p.accessToken = token
	p.expiry = expiry
	return token, nil
}

func (p *oauth2Provider) Invalidate() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.accessToken = ""
	p.expiry = time.Time{}
}

func (p *oauth2Provider) fetchToken() (string, time.Time, error) {
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {p.cfg.ClientID},
		"client_secret": {p.cfg.ClientSecret},
	}
	if len(p.cfg.Scopes) > 0 {
		form.Set("scope", strings.Join(p.cfg.Scopes, " "))
	}

	resp, err := http.PostForm(p.cfg.TokenURL, form)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("oauth2 token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("oauth2 read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("oauth2 token error (HTTP %d): %s",
			resp.StatusCode, truncateBytes(body, 200))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", time.Time{}, fmt.Errorf("oauth2 parse response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return "", time.Time{}, fmt.Errorf("oauth2: empty access_token in response")
	}

	expiry := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	if tokenResp.ExpiresIn <= 0 {
		// Default to 1 hour if server doesn't specify
		expiry = time.Now().Add(1 * time.Hour)
	}

	return tokenResp.AccessToken, expiry, nil
}

// --- Command Token Provider ---

type commandProvider struct {
	command string
	args    []string

	mu          sync.Mutex
	accessToken string
	fetched     time.Time
	ttl         time.Duration
}

// NewCommandProvider creates a TokenProvider that executes an external
// command to obtain an access token. The command's stdout is trimmed
// and used as the Bearer token.
//
// Tokens are cached for the given TTL duration. Use 0 for no caching
// (fetch on every call).
//
// Examples:
//
//	NewCommandProvider("gcloud", []string{"auth", "print-access-token"}, 5*time.Minute)
//	NewCommandProvider("vault", []string{"read", "-field=token", "secret/mcp"}, 0)
func NewCommandProvider(command string, args []string, ttl time.Duration) TokenProvider {
	return &commandProvider{
		command: command,
		args:    args,
		ttl:     ttl,
	}
}

func (p *commandProvider) Token() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Return cached token if still valid
	if p.accessToken != "" && p.ttl > 0 && time.Since(p.fetched) < p.ttl {
		return p.accessToken, nil
	}

	cmd := exec.Command(p.command, p.args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("token command failed: %w\nstderr: %s",
				err, truncateBytes(exitErr.Stderr, 200))
		}
		return "", fmt.Errorf("token command failed: %w", err)
	}

	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", fmt.Errorf("token command returned empty output")
	}

	p.accessToken = token
	p.fetched = time.Now()
	return token, nil
}

func (p *commandProvider) Invalidate() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.accessToken = ""
	p.fetched = time.Time{}
}
